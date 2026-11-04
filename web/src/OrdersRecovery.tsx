import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api, post } from "./api";
import { OrdersPositionPatch } from "./OrdersPositionPatch";
import {
  MoyOrderLink,
  orderActionLabel,
  orderDate,
  orderFieldText,
  orderRequestError,
  orderScopeBody,
  ordersURL,
  sameOrderScope,
  type NativeScope,
  type OrderField,
  type OrderScope,
  type OrderWork,
} from "./OrdersTypes";

type RecoveryView = {
  work: OrderWork & { scope: NativeScope };
  eligible: boolean;
  blockers: { code: string }[];
  evidence?: {
    id: string;
    scope: NativeScope;
    work_id: string;
    code: string;
    created_at: string;
    expires_at: string;
    source?: { status: OrderField<string>; observed_at: string };
    target?: {
      observed_at: string;
      complete: boolean;
      positions: {
        id: string;
        quantity: string;
        reserve: OrderField<string>;
      }[];
    };
  };
  resolution?: {
    id: string;
    created_at: string;
    accepted_uncertainty: boolean;
    new_operation_id?: string;
    attestation?: {
      id: string;
      valid_until: string;
      new_operation_id?: string;
      new_operation_state?: string;
    };
  };
};
const blockers: Record<string, string> = {
  RECOVERY_CHECK_REQUIRED:
    "Запросите свежую сверку источника и заказа МойСклад.",
  RECOVERY_EVIDENCE_STALE: "Срок сверки истёк. Запросите новую проверку.",
  RECOVERY_SOURCE_REFRESH_REQUIRED:
    "Обновите источник штатной проверкой заказов, затем повторите сверку.",
  RECOVERY_EXACT_NATIVE_REQUIRED:
    "Точный связанный заказ не подтверждён. Повторное создание запрещено.",
  RECOVERY_ACTIVE_CLAIM_OR_PROVIDER_CALL:
    "Есть активная обработка или запрос провайдера. Дождитесь завершения и остановите прежний обработчик перед восстановлением.",
  RECOVERY_ATTESTATION_STILL_ACTIVE:
    "Разрешение уже действует. Создайте новый предпросмотр в списке отправлений.",
  RECOVERY_INSPECT_NEW_OPERATION:
    "Уже создана новая операция. Проверяйте её результат; прежнее разрешение повторно не используется.",
  UNKNOWN_ACTION_DISCOVERY_ONLY:
    "Для этого действия доступна только проверка. Автоматическая повторная отправка и освобождение блокировки запрещены.",
  UNKNOWN_NATIVE_KIND_DISCOVERY_ONLY:
    "Для складского документа доступна только проверка; новая мутация требует отдельного разрешённого процесса.",
};
function validateView(value: RecoveryView, scope: OrderScope, workID: string) {
  if (
    value.work.id !== workID ||
    !sameOrderScope(value.work.scope, scope) ||
    (value.evidence &&
      (value.evidence.work_id !== workID ||
        !sameOrderScope(value.evidence.scope, scope)))
  )
    throw new Error("recovery scope mismatch");
  return value;
}

export function OrdersRecovery({
  scope,
  work,
  onAuthorization,
}: {
  scope: OrderScope;
  work: OrderWork;
  onAuthorization?: (posting: string, until: string | undefined) => void;
}) {
  const [open, setOpen] = useState(false);
  const [reason, setReason] = useState("");
  const [acknowledged, setAcknowledged] = useState(false);
  const [stopped, setStopped] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [result, setResult] = useState<RecoveryView>();
  const [tick, setTick] = useState(Date.now);
  const lock = useRef(false);
  const intent = useRef<{ evidence: string; key: string } | undefined>(
    undefined,
  );
  const view = useQuery({
    queryKey: ["orders-recovery", scope, work.id],
    enabled: open,
    queryFn: async ({ signal }) =>
      validateView(
        await api<RecoveryView>(
          ordersURL("recovery", scope, { work_id: work.id }),
          signal,
        ),
        scope,
        work.id,
      ),
    retry: false,
    refetchOnWindowFocus: false,
  });
  const value = result ?? view.data;
  const evidence = value?.evidence;
  const until = value?.resolution?.attestation?.valid_until;
  const deadline = Math.min(
    ...[evidence?.expires_at, until].flatMap((date) =>
      date && Date.parse(date) > Date.now() ? [Date.parse(date)] : [],
    ),
  );
  useEffect(() => {
    if (!Number.isFinite(deadline)) return;
    const timer = window.setTimeout(
      () => setTick(Date.now()),
      Math.min(deadline - Date.now() + 1, 2147483647),
    );
    return () => window.clearTimeout(timer);
  }, [deadline, tick]);
  useEffect(() => {
    if (!value?.resolution?.accepted_uncertainty) return;
    onAuthorization?.(
      work.command.posting_number,
      until && Date.parse(until) > Date.now() ? until : undefined,
    );
  }, [value, until, tick, work.command.posting_number, onAuthorization]);
  const fresh = !!evidence && Date.parse(evidence.expires_at) > Date.now();
  const allowed =
    value?.eligible &&
    fresh &&
    reason.trim().length >= 16 &&
    acknowledged &&
    stopped &&
    !busy &&
    !view.isFetching;
  async function execute(kind: "check" | "resolve") {
    if (lock.current || (kind === "resolve" && !allowed)) return;
    lock.current = true;
    setBusy(true);
    setError("");
    try {
      const body = { ...orderScopeBody(scope), work_id: work.id };
      let request: unknown = body;
      if (kind === "resolve" && evidence) {
        if (intent.current?.evidence !== evidence.id)
          intent.current = { evidence: evidence.id, key: crypto.randomUUID() };
        request = {
          ...body,
          evidence_id: evidence.id,
          idempotency_key: intent.current.key,
          reason,
          acknowledge: true,
          worker_stopped: true,
        };
      }
      const response = validateView(
        await post<RecoveryView>(
          `/api/orders/recovery/${kind}`,
          request,
          AbortSignal.timeout(30000),
        ),
        scope,
        work.id,
      );
      setResult(response);
      setAcknowledged(false);
      setStopped(false);
      if (kind === "resolve") setReason("");
    } catch (failure) {
      setError(
        orderRequestError(failure) +
          " Не повторяйте внешнюю запись; обновите сведения о восстановлении.",
      );
    } finally {
      lock.current = false;
      setBusy(false);
    }
  }
  return (
    <details
      className="orders-action-form"
      onToggle={(event) => setOpen(event.currentTarget.open)}
    >
      <summary>
        Проверить неизвестный результат и варианты восстановления
      </summary>
      <div className="orders-detail-body">
        <p className="orders-warning">
          Исходная операция останется с неизвестным результатом. Разрешение
          нового решения не отменяет старый HTTP-запрос и не гарантирует порядок
          применения у провайдера.
        </p>
        <p>
          {orderActionLabel(work.command.action)} ·{" "}
          {work.command.posting_number}
        </p>
        {work.command.position_patch && (
          <OrdersPositionPatch value={work.command.position_patch} />
        )}
        <MoyOrderLink id={work.command.correlation?.customer_order_id} />
        {view.isLoading && <p role="status">Читаем историю восстановления…</p>}
        {(error || view.isError) && (
          <p role="alert">{error || orderRequestError(view.error)}</p>
        )}
        <div className="orders-actions">
          <button
            type="button"
            className="secondary"
            disabled={busy || view.isFetching}
            onClick={() => void execute("check")}
          >
            Сверить результат чтением
          </button>
          <button
            type="button"
            className="secondary"
            disabled={busy || view.isFetching}
            onClick={() => {
              setResult(undefined);
              void view.refetch();
            }}
          >
            Обновить сведения о восстановлении
          </button>
        </div>
        {evidence && (
          <>
            <p>
              Проверка: {orderDate(evidence.created_at)}. Действует до{" "}
              {orderDate(evidence.expires_at)}.
            </p>
            <p>
              Источник проверен: {orderDate(evidence.source?.observed_at)}.
              Статус: {orderFieldText(evidence.source?.status)}.
            </p>
            <p>
              МойСклад проверен: {orderDate(evidence.target?.observed_at)}.{" "}
              {evidence.target?.complete
                ? "Состав прочитан полностью."
                : "Полнота состава не подтверждена."}
            </p>
            {(evidence.target?.positions ?? []).slice(0, 25).map((line) => (
              <p key={line.id}>
                Позиция {line.id}: количество {line.quantity}, резерв{" "}
                {orderFieldText(line.reserve)}.
              </p>
            ))}
            {(evidence.target?.positions.length ?? 0) > 25 && (
              <p>
                Показаны первые 25 позиций. Перед разрешением проверьте полный
                заказ в МойСклад.
              </p>
            )}
          </>
        )}
        {(value?.blockers ?? []).map((item, index) => (
          <p className="orders-warning" key={index}>
            {blockers[item.code] ??
              "Условия восстановления не подтверждены. Проверьте настройки, текущий источник и связанную операцию."}
            <details>
              <summary>Код ограничения</summary>
              <code>{item.code}</code>
            </details>
          </p>
        ))}
        {value?.resolution && (
          <p className="orders-note">
            Принятие неопределённости записано{" "}
            {orderDate(value.resolution.created_at)}. История исходной операции
            сохранена.{" "}
            {until && Date.parse(until) > Date.now()
              ? `Новое решение можно проверить до ${orderDate(until)}.`
              : "Срок разрешения истёк."}{" "}
            {value.resolution.attestation?.new_operation_id ||
            value.resolution.new_operation_id
              ? `Новая операция: ${value.resolution.attestation?.new_operation_id || value.resolution.new_operation_id}.`
              : "Теперь создайте отдельный свежий предпросмотр отправления. Запись автоматически не отправляется."}
          </p>
        )}
        {value?.eligible && (
          <fieldset disabled={busy || !fresh}>
            <legend>
              {value.resolution
                ? "Новое ограниченное по времени подтверждение"
                : "Разрешить отдельное новое решение"}
            </legend>
            <label>
              Основание восстановления (не менее 16 символов)
              <textarea
                rows={3}
                maxLength={1000}
                autoComplete="off"
                value={reason}
                onChange={(event) => setReason(event.target.value)}
              />
            </label>
            <label className="orders-check">
              <input
                type="checkbox"
                checked={stopped}
                onChange={(event) => setStopped(event.target.checked)}
              />
              Прежний обработчик остановлен; активная отправка этого заказа
              исключена.
            </label>
            <label className="orders-check">
              <input
                type="checkbox"
                checked={acknowledged}
                onChange={(event) => setAcknowledged(event.target.checked)}
              />
              Принимаю риск позднего применения прежнего запроса. Разрешаю
              только новый предпросмотр и отдельное подтверждение.
            </label>
            <button
              type="button"
              disabled={!allowed}
              onClick={() => void execute("resolve")}
            >
              Записать разрешение нового решения
            </button>
          </fieldset>
        )}
      </div>
    </details>
  );
}
