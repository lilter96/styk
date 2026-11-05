import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "./api";
import {
  knownOrderField,
  orderDate,
  orderRequestError,
  ordersURL,
  sameOrderScope,
  type OrderPosting,
  type OrderScope,
  type OrdersConfigData,
} from "./OrdersTypes";
import type {
  NativeDemandWork,
  NativeDocumentPolicy,
  NativeOperationPage,
  OrderNativeProposal,
} from "./OrdersNativeTypes";
import {
  nativeRetryCandidate,
  nativeRetryLocalTime,
  nativeRetryQuantities,
} from "./OrdersNativeRetry";

type Props = {
  scope: OrderScope;
  posting: OrderPosting;
  disabled: boolean;
  onPreview: (proposal: OrderNativeProposal) => void;
  onDraftChange: () => void;
};
type Mode = "draft" | "demand" | "return";

// Quantities and provider identifiers never pass through floating point.
export function nativeQuantityAtMost(quantity: string, maximum: string) {
  const decimal = /^(?:0|[1-9]\d*)(?:\.\d+)?$/;
  if (
    quantity.length > 128 ||
    maximum.length > 128 ||
    !decimal.test(quantity) ||
    !decimal.test(maximum)
  )
    return false;
  const [whole, fraction = ""] = quantity.split(".");
  const [maxWhole, maxFraction = ""] = maximum.split(".");
  const left = BigInt(whole + fraction),
    right = BigInt(maxWhole + maxFraction);
  return (
    left > 0n &&
    left * 10n ** BigInt(maxFraction.length) <=
      right * 10n ** BigInt(fraction.length)
  );
}

export function nativeQuantityTotalAtMost(
  quantities: string[],
  maximum: string,
) {
  if (
    quantities.length === 0 ||
    quantities.length > 100 ||
    quantities.some((value) => !nativeQuantityAtMost(value, maximum))
  )
    return false;
  const parts = quantities.map((value) => value.split("."));
  const [whole, fraction = ""] = maximum.split(".");
  const scale = Math.max(
    fraction.length,
    ...parts.map((part) => part[1]?.length ?? 0),
  );
  const total = parts.reduce(
    (sum, [integer, decimal = ""]) =>
      sum + BigInt(integer + decimal) * 10n ** BigInt(scale - decimal.length),
    0n,
  );
  return (
    total <= BigInt(whole + fraction) * 10n ** BigInt(scale - fraction.length)
  );
}

export function OrdersNativeDocuments(props: Props) {
  if (!sameOrderScope(props.posting.scope, props.scope))
    return (
      <p role="alert">
        Отправление относится к другому подключению. Обновите список заказов.
      </p>
    );
  return (
    <NativeForm
      key={`${props.scope.tenant}:${props.scope.moy}:${props.scope.ozon}:${props.posting.posting_number}:${props.posting.evidence.revision}`}
      {...props}
    />
  );
}

function NativeForm({
  scope,
  posting,
  disabled,
  onPreview,
  onDraftChange,
}: Props) {
  const [open, setOpen] = useState(false);
  const [mode, setMode] = useState<Mode>("draft");
  const [quantities, setQuantities] = useState<Record<number, string>>({});
  const [planned, setPlanned] = useState("");
  const [occurred, setOccurred] = useState("");
  const [reference, setReference] = useState("");
  const [confirmed, setConfirmed] = useState(false);
  const [basis, setBasis] = useState("");
  const [cursors, setCursors] = useState([""]);
  const [now, setNow] = useState(Date.now);
  const [error, setError] = useState("");
  const [retryEnabled, setRetryEnabled] = useState(false);
  const [retryID, setRetryID] = useState("");
  const config = useQuery({
    queryKey: ["orders-config", scope],
    queryFn: async ({ signal }) => {
      const data = await api<
        OrdersConfigData & { native_documents?: NativeDocumentPolicy }
      >(ordersURL("config", scope), signal);
      if (!sameOrderScope(data.scope, scope))
        throw new Error("native configuration account mismatch");
      return data;
    },
    enabled: open,
    retry: false,
    refetchOnWindowFocus: false,
  });
  const cursor = cursors[cursors.length - 1];
  const history = useQuery({
    queryKey: ["orders-native-demands", scope, posting.posting_number, cursor],
    queryFn: async ({ signal }) => {
      const data = await api<NativeOperationPage>(
        ordersURL("operations", scope, {
          search: posting.posting_number,
          limit: "25",
          cursor,
        }),
        signal,
      );
      if (
        (data.operations ?? []).some((op) => !sameOrderScope(op.scope, scope))
      )
        throw new Error("native document account mismatch");
      return data;
    },
    enabled: open && (mode === "return" || retryEnabled),
    retry: false,
    refetchOnWindowFocus: false,
  });
  const policy = config.data?.native_documents;
  const expiry = Date.parse(policy?.valid_until ?? "");
  const postedAllowed =
    !!policy?.allow_posted &&
    Date.parse(policy.attested_at) <= now &&
    expiry > now;
  const eventMilliseconds = Date.parse(occurred);
  const eventExpiry =
    mode !== "draft" &&
    Number.isFinite(eventMilliseconds) &&
    (policy?.event_max_age_seconds ?? 0) > 0
      ? eventMilliseconds + (policy?.event_max_age_seconds ?? 0) * 1000
      : NaN;
  useEffect(() => {
    const deadlines = [expiry, eventExpiry].filter(
      (value) => Number.isFinite(value) && value > Date.now(),
    );
    if (deadlines.length === 0) return;
    const deadline = Math.min(...deadlines);
    const timer = window.setTimeout(
      () => {
        setNow(Date.now());
        onDraftChange();
      },
      Math.min(deadline - Date.now() + 1, 2147483647),
    );
    return () => window.clearTimeout(timer);
  }, [expiry, eventExpiry, now, onDraftChange]);
  const demands = (history.data?.operations ?? []).flatMap((op) =>
    op.targets
      .filter((work) => eligibleDemand(work, posting.posting_number, scope))
      .map((work) => ({ work, at: op.created_at })),
  );
  const retries = (history.data?.operations ?? []).flatMap((op) =>
    op.targets
      .filter((work) =>
        nativeRetryCandidate(work, posting.posting_number, scope),
      )
      .map((work) => ({ work, at: op.created_at })),
  );
  const retryWork = retryEnabled
    ? retries.find(({ work }) => work.id === retryID)?.work
    : undefined;
  const action = mode === "return" ? "salesreturn_create" : "demand_create";
  const available =
    config.data?.enabled &&
    config.data.test_enabled &&
    config.data.publication_available === true &&
    !!policy?.enabled &&
    policy.actions?.includes(action) === true &&
    (mode === "draft" || postedAllowed);
  const lines = posting.lines.flatMap((line, index) =>
    quantities[index]?.trim()
      ? [{ identity: line.identity, quantity: quantities[index].trim() }]
      : [],
  );
  const totalValid = nativeQuantityTotalAtMost(
    lines.map((line) => line.quantity),
    policy?.maximum_quantity ?? "0",
  );
  const quantitiesValid =
    totalValid &&
    posting.lines.every(
      (line, index) =>
        !quantities[index]?.trim() ||
        (nativeQuantityAtMost(quantities[index].trim(), line.quantity) &&
          nativeQuantityAtMost(
            quantities[index].trim(),
            policy?.maximum_quantity ?? "0",
          )),
    );
  const basisValid =
    mode !== "return" ||
    (!!retryWork && !!retryWork.command.native_movement?.demand_work_id) ||
    (!!demands.find(({ work }) => work.id === basis) &&
      !history.isFetching &&
      !history.isError);
  const eventTime = mode === "draft" ? planned : occurred;
  const milliseconds = Date.parse(eventTime);
  const timeValid =
    Number.isFinite(milliseconds) &&
    (mode === "draft" ||
      (milliseconds <= Date.now() + 60000 &&
        Date.now() - milliseconds <
          (policy?.event_max_age_seconds ?? 0) * 1000));
  const canPreview =
    !disabled &&
    !!available &&
    quantitiesValid &&
    basisValid &&
    timeValid &&
    (retryEnabled
      ? !!retryWork && !history.isFetching && !history.isError
      : reference.trim().length > 0 && reference.trim().length <= 4096) &&
    (mode === "draft" || confirmed);
  function changed() {
    onDraftChange();
    setError("");
  }
  function submit(event: React.FormEvent) {
    event.preventDefault();
    if (!canPreview) {
      setError("Проверьте разрешение, состав, время и подтверждение события.");
      return;
    }
    const time = new Date(eventTime).toISOString();
    onPreview({
      posting_number: posting.posting_number,
      action,
      applicable: {
        presence: mode === "draft" ? "zero" : "value",
        value: mode !== "draft",
      },
      lines,
      event_kind:
        mode === "draft"
          ? "draft_plan"
          : mode === "demand"
            ? "shipment_dispatched"
            : "return_received",
      event_confirmed: mode !== "draft" && confirmed,
      ...(retryWork
        ? { retry_work_id: retryWork.id }
        : { event_reference: reference.trim() }),
      ...(mode === "draft"
        ? { planned_moment: time }
        : retryWork
          ? {}
          : { event_occurred_at: time }),
      ...(mode === "return" ? { demand_work_id: basis } : {}),
    });
  }
  function chooseRetry(id: string) {
    const work = retries.find((item) => item.work.id === id)?.work;
    setRetryID(id);
    setConfirmed(false);
    setReference("");
    setMode("draft");
    setBasis("");
    setOccurred("");
    setPlanned("");
    setQuantities(work ? nativeRetryQuantities(posting.lines, work) : {});
    if (work?.command.native_movement) {
      const movement = work.command.native_movement;
      setMode(
        knownOrderField(movement.applicable) !== true
          ? "draft"
          : work.command.action === "salesreturn_create"
            ? "return"
            : "demand",
      );
      setOccurred(nativeRetryLocalTime(movement.event?.occurred_at));
      setPlanned(nativeRetryLocalTime(movement.moment));
      setBasis(movement.demand_work_id ?? "");
    }
    changed();
  }
  return (
    <details
      open={open}
      onToggle={(event) => setOpen(event.currentTarget.open)}
    >
      <summary>Отгрузка и возврат в МойСклад</summary>
      <p className="orders-note">
        Сначала создаётся предпросмотр. Документ появится только после
        отдельного подтверждения. Это не вызов курьера и не платёж.
      </p>
      {config.isLoading && (
        <p role="status">Проверяем разрешение на документы…</p>
      )}
      {config.isError && <p role="alert">{orderRequestError(config.error)}</p>}
      {!config.isLoading && !config.isError && !available && (
        <p className="orders-warning">
          Для выбранного действия нужно действующее разрешение в настройках
          заказов. Проведение требует отдельного разрешения и реального
          физического события.
        </p>
      )}
      <form onSubmit={submit} aria-label="Документ движения МойСклад">
        <fieldset disabled={disabled || config.isLoading || config.isError}>
          <legend>Действие</legend>
          <label className="orders-check">
            <input
              type="checkbox"
              checked={retryEnabled}
              onChange={(event) => {
                setRetryEnabled(event.target.checked);
                chooseRetry("");
                setCursors([""]);
              }}
            />
            Подготовить повтор после отказа API
          </label>
          {retryEnabled && (
            <fieldset>
              <legend>Ранее отклонённая операция</legend>
              <p className="orders-note">
                Будет создана новая операция с прежним идентификатором события.
                Сервер отдельно проверит, что все предыдущие попытки отклонены
                без применения. Неизвестный результат повторять здесь нельзя.
              </p>
              {history.isLoading && (
                <p role="status">Ищем отклонённые операции…</p>
              )}
              {history.isError && (
                <p role="alert">{orderRequestError(history.error)}</p>
              )}
              <label>
                Операция для проверки повторной попытки
                <select
                  value={retryID}
                  disabled={history.isFetching || history.isError}
                  onChange={(event) => chooseRetry(event.target.value)}
                >
                  <option value="">Выберите отклонённую операцию</option>
                  {retries.map(({ work, at }) => (
                    <option key={work.id} value={work.id}>
                      {work.command.action === "salesreturn_create"
                        ? "Возврат"
                        : "Отгрузка"}{" "}
                      от {orderDate(at)} ·{" "}
                      {work.receipt.http_status
                        ? `HTTP ${work.receipt.http_status}`
                        : "До отправки"}
                    </option>
                  ))}
                </select>
              </label>
              {!history.isLoading && !history.isError && !retries.length && (
                <p>
                  На этой странице нет подходящих отказов. Неизвестный результат
                  требует отдельной сверки в истории операций.
                </p>
              )}
              <div className="orders-actions">
                <button
                  type="button"
                  className="secondary"
                  disabled={history.isFetching || cursors.length === 1}
                  onClick={() => {
                    setCursors(cursors.slice(0, -1));
                    chooseRetry("");
                  }}
                >
                  Предыдущие операции
                </button>
                <button
                  type="button"
                  className="secondary"
                  disabled={history.isFetching || !history.data?.next_cursor}
                  onClick={() => {
                    setCursors([...cursors, history.data?.next_cursor ?? ""]);
                    chooseRetry("");
                  }}
                >
                  Ещё операции
                </button>
              </div>
            </fieldset>
          )}
          <label>
            Документ
            <select
              disabled={retryEnabled}
              value={mode}
              onChange={(event) => {
                setMode(event.target.value as Mode);
                setConfirmed(false);
                setBasis("");
                setReference("");
                changed();
              }}
            >
              <option value="draft">
                Черновик отгрузки — без движения товара
              </option>
              <option value="demand">Провести фактическую отгрузку</option>
              <option value="return">Принять фактический возврат</option>
            </select>
          </label>
          {mode !== "draft" && (
            <p className="orders-warning">
              Проведение изменит физический остаток МойСклад. Статус Ozon не
              доказывает передачу или возврат товара. Чужие резервы не
              освобождаются.
            </p>
          )}
          {mode === "return" && !retryEnabled && (
            <fieldset>
              <legend>Исходная отгрузка STYK</legend>
              {history.isLoading && (
                <p role="status">Ищем подтверждённые отгрузки…</p>
              )}
              {history.isError && (
                <p role="alert">{orderRequestError(history.error)}</p>
              )}
              <label>
                Отгрузка этого отправления
                <select
                  value={basis}
                  disabled={history.isFetching || history.isError}
                  onChange={(event) => {
                    setBasis(event.target.value);
                    changed();
                  }}
                >
                  <option value="">Выберите подтверждённую отгрузку</option>
                  {demands.map(({ work, at }) => (
                    <option key={work.id} value={work.id}>
                      Отгрузка от{" "}
                      {orderDate(work.native_document?.observed_at ?? at)} ·{" "}
                      {work.native_document?.id ?? work.receipt.external_id}
                    </option>
                  ))}
                </select>
              </label>
              {!history.isLoading && !history.isError && !demands.length && (
                <p>
                  На этой странице нет проведённой отгрузки этого отправления.
                  Черновик или неизвестный результат не являются основанием для
                  возврата.
                </p>
              )}
              <div className="orders-actions">
                <button
                  type="button"
                  className="secondary"
                  disabled={history.isFetching || cursors.length === 1}
                  onClick={() => {
                    setCursors(cursors.slice(0, -1));
                    setBasis("");
                    changed();
                  }}
                >
                  Назад по отгрузкам
                </button>
                <button
                  type="button"
                  className="secondary"
                  disabled={history.isFetching || !history.data?.next_cursor}
                  onClick={() => {
                    setCursors([...cursors, history.data?.next_cursor ?? ""]);
                    setBasis("");
                    changed();
                  }}
                >
                  Ещё отгрузки
                </button>
              </div>
            </fieldset>
          )}
          <fieldset>
            <legend>Товары документа</legend>
            <p className="orders-note">
              Общий лимит документа:{" "}
              {policy?.maximum_quantity ?? "не установлен"}.
            </p>
            {lines.length > 0 && !totalValid && (
              <p className="orders-warning">
                Суммарное количество должно быть положительным и не превышать
                общий лимит документа.
              </p>
            )}
            <p className="orders-note">
              Укажите количество только у нужных позиций. Пустое поле исключает
              товар. Остаток по отгрузке и возвраты дополнительно проверит
              сервер.
            </p>
            {posting.lines.map((line, index) => (
              <label key={JSON.stringify(line.identity)}>
                Количество ·{" "}
                {knownOrderField(line.name) ??
                  line.identity.offer_id ??
                  line.identity.sku ??
                  "Товар"}
                <input
                  type="text"
                  inputMode="decimal"
                  autoComplete="off"
                  value={quantities[index] ?? ""}
                  placeholder="Не включать"
                  aria-invalid={
                    !!quantities[index] &&
                    (!nativeQuantityAtMost(
                      quantities[index].trim(),
                      line.quantity,
                    ) ||
                      !nativeQuantityAtMost(
                        quantities[index].trim(),
                        policy?.maximum_quantity ?? "0",
                      ))
                  }
                  onChange={(event) => {
                    setQuantities({
                      ...quantities,
                      [index]: event.target.value,
                    });
                    changed();
                  }}
                />
                <span className="orders-note">
                  В отправлении: {line.quantity}; лимит разрешения:{" "}
                  {policy?.maximum_quantity ?? "не установлен"}.
                </span>
              </label>
            ))}
          </fieldset>
          <label>
            {mode === "draft"
              ? "Плановое время документа"
              : "Фактическое время события"}
            <input
              type="datetime-local"
              disabled={!!retryWork && mode !== "draft"}
              step="0.001"
              value={eventTime}
              onChange={(event) => {
                if (mode === "draft") setPlanned(event.target.value);
                else setOccurred(event.target.value);
                changed();
              }}
            />
          </label>
          <p className="orders-note">
            {retryWork && mode !== "draft"
              ? "Сохранено время исходного физического события. Новая попытка API не меняет время передачи или возврата товара."
              : "Время указывается в часовом поясе вашего браузера. Оно не подставляется из даты заказа или статуса доставки."}
          </p>
          {!retryEnabled && (
            <label>
              {mode === "draft"
                ? "Идентификатор планирования"
                : "Идентификатор реального события"}
              <input
                type="password"
                autoComplete="off"
                maxLength={4096}
                value={reference}
                onChange={(event) => {
                  setReference(event.target.value);
                  changed();
                }}
              />
            </label>
          )}
          <p className="orders-note">
            {retryEnabled
              ? "Исходный идентификатор события будет восстановлен сервером из защищённой записи. Повторно вводить или менять его не нужно; отказ и новая попытка останутся в истории."
              : "Используйте уникальный внутренний номер акта или события без персональных данных. Он сохраняется зашифрованным; повторное событие не создаёт второй документ."}
          </p>
          {mode !== "draft" && (
            <label className="orders-check">
              <input
                type="checkbox"
                checked={confirmed}
                onChange={(event) => {
                  setConfirmed(event.target.checked);
                  changed();
                }}
              />
              {mode === "demand"
                ? "Подтверждаю: выбранный товар фактически передан со склада."
                : "Подтверждаю: выбранный товар фактически получен обратно на склад."}
            </label>
          )}
          <p className="orders-note">
            НДС, валюта и цена берутся из проверенного заказа или исходной
            отгрузки. Подтверждённый НДС, уже включённый в цену, сохраняется без
            повторного начисления. Неподдерживаемая маркировка, неизвестные поля
            и расхождения блокируют только это действие.
          </p>
          {error && <p role="alert">{error}</p>}
          <button type="submit" disabled={!canPreview}>
            Проверить документ перед созданием
          </button>
        </fieldset>
      </form>
    </details>
  );
}

function eligibleDemand(
  work: NativeDemandWork,
  posting: string,
  scope: OrderScope,
) {
  const movement = work.command.native_movement;
  const nativeID = work.native_document?.id ?? work.receipt.external_id;
  return (
    sameOrderScope(work.scope, scope) &&
    sameOrderScope(work.command.scope, scope) &&
    work.state === "RECONCILED" &&
    (!work.native_document || work.native_document.kind === "demand") &&
    work.command.action === "demand_create" &&
    work.command.posting_number === posting &&
    movement?.correlation.kind === "demand" &&
    knownOrderField(movement.applicable) === true &&
    /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(
      nativeID ?? "",
    )
  );
}
