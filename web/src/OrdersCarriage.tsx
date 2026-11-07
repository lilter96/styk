import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api, ApiError, post } from "./api";
import {
  carriageIssue,
  carriageState,
  type CarriageAction,
  type CarriagePlan,
  type CarriagePreview,
  type CarriageWork,
} from "./OrdersCarriageTypes";
import {
  orderDate,
  orderRequestError,
  orderScopeBody,
  ordersURL,
  sameOrderScope,
  type OrderScope,
  type OrdersConfigData,
} from "./OrdersTypes";
type Pending = {
  preview_id: string;
  idempotency_key: string;
  action: CarriageAction;
};
const key = (scope: OrderScope) =>
  `styk-carriage-pending:${JSON.stringify(orderScopeBody(scope))}`;
function loadPending(scope: OrderScope): Pending | null {
  try {
    const value = JSON.parse(sessionStorage.getItem(key(scope)) ?? "null");
    return value &&
      typeof value.preview_id === "string" &&
      typeof value.idempotency_key === "string" &&
      ["CREATE", "APPROVE"].includes(value.action)
      ? value
      : null;
  } catch {
    return null;
  }
}
function clearPending(scope: OrderScope) {
  try {
    sessionStorage.removeItem(key(scope));
    return true;
  } catch {
    return false;
  }
}
const positiveID = (value: string) => /^[1-9]\d{0,18}$/.test(value);
function validPlan(plan: CarriagePlan, scope: OrderScope) {
  return (
    !!plan?.id &&
    sameOrderScope(plan.scope, scope) &&
    ["CREATE", "APPROVE"].includes(plan.action) &&
    Array.isArray(plan.members) &&
    plan.members.every(
      (member) =>
        typeof member.posting_number === "string" &&
        typeof member.ready === "boolean",
    )
  );
}
function validWork(work: CarriageWork, scope: OrderScope) {
  return (
    !!work?.id &&
    sameOrderScope(work.scope, scope) &&
    validPlan(work.plan, scope)
  );
}
function CarriageBatch({ plan }: { plan: CarriagePlan }) {
  return (
    <>
      <h4>
        {plan.action === "CREATE"
          ? "Создание всей готовой партии"
          : "Подтверждение созданной партии"}
      </h4>
      <dl className="settings-realization-facts">
        <div>
          <dt>Способ доставки Ozon</dt>
          <dd>{plan.delivery_method_id}</dd>
        </div>
        <div>
          <dt>Склад Ozon</dt>
          <dd>{plan.warehouse_id || "Не подтверждён"}</dd>
        </div>
        <div>
          <dt>Отправлений</dt>
          <dd>{plan.members.length}</dd>
        </div>
        {plan.carriage_id && (
          <div>
            <dt>Партия Ozon</dt>
            <dd>{plan.carriage_id}</dd>
          </div>
        )}
        {plan.snapshot?.header && (
          <>
            <div>
              <dt>Наблюдаемый статус Ozon</dt>
              <dd>{plan.snapshot.header.status || "Неизвестен"}</dd>
            </div>
            <div>
              <dt>Доступные действия Ozon</dt>
              <dd>
                {plan.snapshot.header.available_actions?.join(", ") ||
                  "Не подтверждены"}
              </dd>
            </div>
          </>
        )}
        {plan.departure_date && (
          <div>
            <dt>Дата выезда</dt>
            <dd>{orderDate(plan.departure_date)}</dd>
          </div>
        )}
        {plan.all_blr_traceable !== undefined && (
          <div>
            <dt>Все товары прослеживаемые в Беларуси</dt>
            <dd>
              {plan.all_blr_traceable
                ? "Да — заявлено владельцем"
                : "Нет — заявлено владельцем"}
            </dd>
          </div>
        )}
        {plan.containers_count !== undefined && (
          <div>
            <dt>Грузовых мест</dt>
            <dd>{plan.containers_count}</dd>
          </div>
        )}
        {plan.snapshot?.header && (
          <div>
            <dt>Профиль передачи Ozon</dt>
            <dd>
              {plan.snapshot.header.first_mile_type || "Не подтверждён"} /{" "}
              {plan.snapshot.header.integration_type || "Не подтверждён"}
            </dd>
          </div>
        )}
        <div>
          <dt>Версия настроек</dt>
          <dd>{plan.config_revision}</dd>
        </div>
        <div>
          <dt>Состав</dt>
          <dd>
            <code title={plan.membership_fingerprint}>
              {plan.membership_fingerprint.slice(0, 12)}…
            </code>
          </dd>
        </div>
        <div>
          <dt>Действует до</dt>
          <dd>{orderDate(plan.expires_at)}</dd>
        </div>
      </dl>
      <p className="settings-warning">
        Ozon может включить все готовые отправления этого способа доставки.
        Проверьте весь список. Изменение состава требует новой проверки и
        отдельного согласия.
      </p>
      <details>
        <summary>Полный состав партии ({plan.members.length})</summary>
        <ul className="settings-realization-rows">
          {plan.members.map((member) => (
            <li key={member.posting_number}>
              <strong>{member.posting_number}</strong>
              <span>
                Статус Ozon: {member.status || "Неизвестен"}; готовность:{" "}
                {member.ready
                  ? "подтверждена для этой операции"
                  : "не подтверждена"}
                .
              </span>
              {(member.lines ?? []).map((line, index) => (
                <span key={index}>
                  SKU: {line.identity.sku || "Не подтверждён"}; артикул:{" "}
                  {line.identity.offer_id || "Не предоставлен"}; количество:{" "}
                  {line.quantity}.
                </span>
              ))}
              {(member.blockers ?? []).map((issue) => (
                <span key={issue.code}>
                  {carriageIssue(issue.code, issue.field)}{" "}
                  <code>{issue.code}</code>
                </span>
              ))}
            </li>
          ))}
        </ul>
        {plan.members.length === 0 && (
          <p>
            Подтверждённых отправлений нет. Пустой список не даёт разрешения на
            запись.
          </p>
        )}
      </details>
    </>
  );
}
function CarriageResult({
  work,
  busy,
  onRead,
}: {
  work: CarriageWork;
  busy: boolean;
  onRead: () => void;
}) {
  return (
    <article className="settings-commission-work">
      <h4>
        {work.plan.action === "CREATE"
          ? "Создание партии"
          : "Подтверждение партии"}
        {work.carriage_id ? ` ${work.carriage_id}` : ""}
      </h4>
      <p>
        <strong>{carriageState(work)}</strong>
      </p>
      <p>
        Формирование партии не подтверждает физическую передачу перевозчику,
        доставку, выплату или проведение Отгрузки МойСклад.
      </p>
      {work.code && ["BLOCKED", "REJECTED", "UNKNOWN"].includes(work.state) && (
        <p className="settings-warning">
          {carriageIssue(work.code)} <code>{work.code}</code>
        </p>
      )}
      <p>Обновлено: {orderDate(work.updated_at)}.</p>
      <button
        type="button"
        className="secondary"
        disabled={busy}
        onClick={onRead}
      >
        Перечитать результат партии
      </button>
    </article>
  );
}
export function OrdersCarriage({
  scope,
  config,
}: {
  scope: OrderScope;
  config: OrdersConfigData;
}) {
  const [action, setAction] = useState<CarriageAction>("CREATE");
  const [method, setMethod] = useState("");
  const [carriageID, setCarriageID] = useState("");
  const [departure, setDeparture] = useState("");
  const [traceable, setTraceable] = useState("");
  const [containers, setContainers] = useState("");
  const [trust, setTrust] = useState(false);
  const [preview, setPreview] = useState<CarriagePreview | null>(null);
  const [pending, setPending] = useState<Pending | null>(() =>
    loadPending(scope),
  );
  const [consent, setConsent] = useState(false);
  const [readyConsent, setReadyConsent] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [now, setNow] = useState(Date.now());
  const lock = useRef(false);
  const history = useQuery({
    queryKey: ["orders-carriage-operations", scope],
    retry: false,
    refetchOnWindowFocus: false,
    refetchInterval: 15000,
    queryFn: async ({ signal }) => {
      const value = await api<{ operations: CarriageWork[] }>(
        ordersURL("carriage/operations", scope, { limit: "25" }),
        signal,
      );
      if (
        !Array.isArray(value.operations) ||
        value.operations.some((work) => !validWork(work, scope))
      )
        throw new Error("carriage history scope mismatch");
      return value.operations;
    },
  });
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, []);
  useEffect(() => {
    if (
      !pending ||
      !history.data?.some((work) => work.preview_id === pending.preview_id)
    )
      return;
    const cleared = clearPending(scope);
    setPending(null);
    if (!cleared)
      setError(
        "Операция найдена, но локальная запись браузера недоступна. Проверьте историю и хранение данных сайта.",
      );
  }, [history.data, pending, scope]);
  const plan = preview?.plan;
  const expired =
    !plan?.expires_at ||
    !Number.isFinite(Date.parse(plan.expires_at)) ||
    Date.parse(plan.expires_at) <= now;
  const canApprove =
    !!plan &&
    plan.config_revision === config.revision &&
    preview?.validation.length === 0 &&
    !expired &&
    !preview.existing_work &&
    plan.members.length > 0 &&
    plan.members.every(
      (member) =>
        member.ready &&
        member.status === "awaiting_deliver" &&
        (member.lines?.length ?? 0) > 0 &&
        (member.blockers?.length ?? 0) === 0,
    ) &&
    consent &&
    readyConsent;
  const validInput =
    positiveID(method) &&
    (action !== "APPROVE" || positiveID(carriageID)) &&
    (action !== "CREATE" || ["true", "false"].includes(traceable)) &&
    (!departure ||
      /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/.test(
        departure,
      )) &&
    (!containers || (/^[1-9]\d{0,8}$/.test(containers) && trust));
  const reset = () => {
    setPreview(null);
    setConsent(false);
    setReadyConsent(false);
  };
  async function check() {
    if (lock.current || pending || !validInput || !config.read_enabled) return;
    lock.current = true;
    setBusy(true);
    setError("");
    reset();
    try {
      const value = await post<CarriagePreview>(
        "/api/orders/carriage/preview",
        {
          ...orderScopeBody(scope),
          expected_revision: config.revision,
          action,
          delivery_method_id: method,
          ...(action === "CREATE"
            ? {
                all_blr_traceable: traceable === "true",
                ...(departure ? { departure_date: departure } : {}),
              }
            : {
                carriage_id: carriageID,
                ...(containers
                  ? {
                      containers_count: Number(containers),
                      trust_acceptance_confirmed: true,
                    }
                  : {}),
              }),
        },
        AbortSignal.timeout(60000),
      );
      if (
        !value?.id ||
        !Array.isArray(value.validation) ||
        !validPlan(value.plan, scope) ||
        value.plan.config_revision !== config.revision ||
        value.plan.action !== action ||
        value.plan.delivery_method_id !== method ||
        (value.existing_work && !validWork(value.existing_work, scope))
      )
        throw new Error("carriage preview scope mismatch");
      setPreview(value);
    } catch (cause) {
      setError(orderRequestError(cause));
    } finally {
      lock.current = false;
      setBusy(false);
    }
  }
  async function approve() {
    if (lock.current || pending || !canApprove || !preview) return;
    const intent = {
      preview_id: preview.id,
      idempotency_key: crypto.randomUUID(),
      action: preview.plan.action,
    };
    try {
      sessionStorage.setItem(key(scope), JSON.stringify(intent));
    } catch {
      setError(
        "Разрешите хранение данных сайта: без сохранённого идентификатора подтверждения запись не отправляется.",
      );
      return;
    }
    lock.current = true;
    setBusy(true);
    setError("");
    setPending(intent);
    try {
      const work = await post<CarriageWork>(
        "/api/orders/carriage/approve",
        {
          ...orderScopeBody(scope),
          preview_id: intent.preview_id,
          idempotency_key: intent.idempotency_key,
          acknowledge: true,
          all_ready_consent: true,
          physical_readiness_confirmed: true,
        },
        AbortSignal.timeout(60000),
      );
      if (!validWork(work, scope) || work.preview_id !== intent.preview_id)
        throw new Error("carriage approval scope mismatch");
      const cleared = clearPending(scope);
      setPending(null);
      reset();
      if (!cleared)
        setError(
          "Подтверждение записано, но локальная запись браузера недоступна. Проверьте историю.",
        );
      void history.refetch();
    } catch (cause) {
      if (
        cause instanceof ApiError &&
        [400, 403, 409, 410, 422].includes(cause.status)
      ) {
        clearPending(scope);
        setPending(null);
        reset();
        setError(orderRequestError(cause));
      } else
        setError(
          "Ответ не получен. Партия могла измениться в Ozon. Повторная запись заблокирована; обновите историю и перечитайте результат.",
        );
    } finally {
      lock.current = false;
      setBusy(false);
    }
  }
  async function reread(work: CarriageWork) {
    if (lock.current) return;
    lock.current = true;
    setBusy(true);
    setError("");
    try {
      await post(
        "/api/orders/carriage/reread",
        { ...orderScopeBody(scope), work_id: work.id },
        AbortSignal.timeout(60000),
      );
      await history.refetch();
    } catch (cause) {
      setError(orderRequestError(cause));
    } finally {
      lock.current = false;
      setBusy(false);
    }
  }
  return (
    <div className="settings-card settings-carriage">
      <h3>Партии отгрузки Ozon FBS</h3>
      <p>
        Продавец собирает заказы в МойСклад. Здесь отдельно проверяются и
        разрешаются создание партии Ozon и её подтверждение. Одна операция не
        запускает следующую автоматически. Для rFBS этот процесс не применяется.
      </p>
      <p className="settings-info">
        Подтверждение партии означает её формирование в Ozon. Физическая
        передача перевозчику и Отгрузка МойСклад требуют отдельного факта и
        политики продавца.
      </p>
      {!config.read_enabled && (
        <p className="settings-warning">
          Включите получение данных Ozon для этой пары аккаунтов.
        </p>
      )}
      <fieldset disabled={busy || !!pending}>
        <legend>Отдельная операция с партией</legend>
        <div className="settings-lifecycle-grid">
          <label>
            Операция партии
            <select
              aria-label="Операция партии"
              value={action}
              onChange={(event) => {
                setAction(event.target.value as CarriageAction);
                reset();
              }}
            >
              <option value="CREATE">Создать готовую партию</option>
              <option value="APPROVE">Подтвердить уже созданную партию</option>
            </select>
          </label>
          <label>
            Идентификатор способа доставки Ozon
            <input
              type="text"
              inputMode="numeric"
              value={method}
              onChange={(event) => {
                setMethod(event.target.value);
                reset();
              }}
            />
          </label>
          {action === "CREATE" ? (
            <>
              <label>
                Дата выезда с часовым поясом (необязательно)
                <input
                  type="text"
                  placeholder="2026-11-10T15:00:00+03:00"
                  value={departure}
                  onChange={(event) => {
                    setDeparture(event.target.value);
                    reset();
                  }}
                />
              </label>
              <label>
                Все товары прослеживаемые в Беларуси
                <select
                  aria-label="Все товары прослеживаемые в Беларуси"
                  value={traceable}
                  onChange={(event) => {
                    setTraceable(event.target.value);
                    reset();
                  }}
                >
                  <option value="">Выберите по фактическому составу</option>
                  <option value="true">
                    Да, подтверждено для всех товаров
                  </option>
                  <option value="false">Нет</option>
                </select>
              </label>
            </>
          ) : (
            <>
              <label>
                Идентификатор созданной партии Ozon
                <input
                  type="text"
                  inputMode="numeric"
                  value={carriageID}
                  onChange={(event) => {
                    setCarriageID(event.target.value);
                    reset();
                  }}
                />
              </label>
              <label>
                Грузовых мест (только при доверительной приёмке)
                <input
                  type="text"
                  inputMode="numeric"
                  value={containers}
                  onChange={(event) => {
                    setContainers(event.target.value);
                    setTrust(false);
                    reset();
                  }}
                />
              </label>
              {containers && (
                <label className="orders-check">
                  <input
                    type="checkbox"
                    checked={trust}
                    onChange={(event) => {
                      setTrust(event.target.checked);
                      reset();
                    }}
                  />
                  Доверительная приёмка по грузовым местам подтверждена для этой
                  партии.
                </label>
              )}
            </>
          )}
        </div>
        <button
          type="button"
          className="secondary"
          disabled={!validInput || !config.read_enabled || history.isError}
          onClick={() => void check()}
        >
          {busy
            ? "Проверяем партию…"
            : action === "CREATE"
              ? "Проверить полную партию"
              : "Проверить созданную партию"}
        </button>
      </fieldset>
      {error && <p role="alert">{error}</p>}
      {pending && (
        <p className="settings-warning" role="status">
          Результат подтверждения пока неизвестен. Создание и повторное
          подтверждение недоступны. Обновление истории и перечитывание не
          отправляют новую запись в Ozon.
        </p>
      )}
      {preview && (
        <section aria-label="Проверка партии Ozon">
          <CarriageBatch plan={preview.plan} />
          {preview.validation.map((issue, index) => (
            <p className="settings-warning" key={`${issue.code}:${index}`}>
              {carriageIssue(issue.code, issue.field)} <code>{issue.code}</code>
            </p>
          ))}
          {preview.plan.config_revision !== config.revision && (
            <p className="settings-warning">
              Настройки изменились после проверки. Получите новый состав партии.
            </p>
          )}
          {expired && (
            <p className="settings-warning">
              Проверка устарела. Получите новый состав перед записью.
            </p>
          )}
          {preview.existing_work && (
            <p className="settings-warning">
              Для этой операции уже есть сохранённая попытка. Проверьте её
              историю; повторная запись недоступна.
            </p>
          )}
          {preview.validation.length === 0 &&
            !expired &&
            !preview.existing_work &&
            preview.plan.members.length > 0 && (
              <>
                <label className="orders-check">
                  <input
                    type="checkbox"
                    checked={consent}
                    disabled={busy || !!pending}
                    onChange={(event) => setConsent(event.target.checked)}
                  />
                  {action === "CREATE"
                    ? "Разрешаю создать всю проверенную партию этого способа доставки, включая все перечисленные отправления."
                    : "Разрешаю подтвердить именно эту созданную партию с проверенным составом отдельной операцией."}
                </label>
                <label className="orders-check">
                  <input
                    type="checkbox"
                    checked={readyConsent}
                    disabled={busy || !!pending}
                    onChange={(event) => setReadyConsent(event.target.checked)}
                  />
                  Подтверждаю фактическую готовность всех отправлений партии.
                  Это не заявление о передаче перевозчику или доставке.
                </label>
                <button
                  type="button"
                  disabled={busy || !!pending || !canApprove}
                  onClick={() => void approve()}
                >
                  {action === "CREATE"
                    ? "Создать партию в Ozon"
                    : "Подтвердить партию в Ozon"}
                </button>
              </>
            )}
          <button
            type="button"
            className="secondary settings-carriage-dismiss"
            disabled={busy || !!pending}
            onClick={reset}
          >
            Закрыть проверку без записи
          </button>
        </section>
      )}
      <div className="settings-card-title">
        <h4>История партий</h4>
        <button
          type="button"
          className="text-action"
          disabled={history.isFetching || busy}
          onClick={() => void history.refetch()}
        >
          Обновить историю партий
        </button>
      </div>
      {history.isFetching && <p role="status">Читаем историю партий…</p>}
      {history.isError && (
        <p role="alert">{orderRequestError(history.error)}</p>
      )}
      {history.data?.length === 0 && (
        <p>Операций с партиями пока нет. Начните с проверки состава.</p>
      )}
      {history.data?.map((work) => (
        <CarriageResult
          key={work.id}
          work={work}
          busy={busy}
          onRead={() => void reread(work)}
        />
      ))}
    </div>
  );
}
