import { useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError, params, post } from "./api";
import { IntegrationDialog } from "./IntegrationDialog";

type Scope = { tenant: string; moysklad_account: string; ozon_account: string };
type Target = {
  ozon_product_id: string;
  ozon_warehouse_id: string;
  offer_id?: string;
  eligible?: boolean;
  source_physical?: string;
  source_reserve?: string;
  marketplace_free?: number;
  quantity?: string;
  last_operation_id?: string;
  last_state?: string;
  readback_quantity?: string;
  readback_at?: string;
};
export type AutomaticRecoveryTarget = {
  operation_id: string;
  ozon_product_id: string;
  ozon_warehouse_id: string;
  quantity: string;
  state: string;
  dispatched_at?: string;
  readback_at?: string;
  readback_quantity?: string;
};
type AutoView = {
  available: boolean;
  policy?: {
    enabled: boolean;
    interval_seconds: number;
    valid_until: string;
    targets: {
      ozon_product_id: string;
      ozon_warehouse_id: string;
      safety_stock: string;
      unreflected: string;
      unseen_bound: string;
      maximum: number;
    }[];
  };
  last_cycle_at?: string;
  next_run_at?: string;
  candidates?: Target[];
  last_cycle?: {
    id: string;
    state: string;
    outcome?: string;
    started_at: string;
    finished_at?: string;
    publication_id?: string;
    metrics: Record<string, unknown>;
  };
  unresolved?: AutomaticRecoveryTarget[];
};

const outcomes: Record<string, string> = {
  NO_CHANGE: "Остатки совпадают — отправка не потребовалась.",
  RECONCILED: "Ozon подтвердил актуальный остаток.",
  PUBLICATION_UNKNOWN:
    "Ответ Ozon не подтверждён. Уточните результат предыдущей отправки.",
  PUBLICATION_REJECTED:
    "Отправка отклонена. Проверьте товар, склад и подключение.",
  RECONCILIATION_PENDING:
    "Ozon ответил, но остаток ещё не подтверждён. Уточните результат.",
  TARGET_UNRESOLVED:
    "Предыдущая отправка ещё не завершена. Проверьте её результат.",
  EVIDENCE_BLOCKED:
    "Недостаточно актуальных данных или изменились соответствия. Проверьте настройки.",
  POLICY_EXPIRED: "Срок тестовых допущений истёк. Подтвердите их заново.",
  AUTHORIZATION_REQUIRED: "Провайдер отклонил доступ. Проверьте подключение.",
  RATE_LIMITED:
    "Провайдер ограничил запросы. Дождитесь разрешённого времени и проверьте подключение.",
  RETRYABLE_READ_FAILURE:
    "Данные временно недоступны. Следующая проверка будет выполнена позже.",
  READ_CONFIGURATION_ERROR:
    "Не удалось прочитать данные. Проверьте подключение.",
  TRAVERSAL_SUPERSEDED: "Данные загрузки изменились. Запустите новую проверку.",
  TARGETS_NEED_ATTENTION:
    "Некоторые товары требуют проверки; остальные продолжают проверяться.",
};

export function AutomaticStockControls({
  scope,
  targets,
  onEnabledChange,
  onRecover,
}: {
  scope: Scope;
  targets: Target[];
  onEnabledChange: (enabled: boolean) => void;
  onRecover?: (target: AutomaticRecoveryTarget) => void;
}) {
  const client = useQueryClient();
  const key = [
    "automatic-stock",
    scope.tenant,
    scope.moysklad_account,
    scope.ozon_account,
  ];
  const query = useQuery({
    queryKey: key,
    queryFn: ({ signal }) =>
      api<AutoView>(`/api/stock-sync/automatic?${params(scope)}`, signal),
    retry: false,
    refetchInterval: 3000,
  });
  const data = query.data,
    enabled = !!data?.policy?.enabled;
  const [open, setOpen] = useState(false),
    [ack, setAck] = useState(false),
    [selected, setSelected] = useState<string[] | null>(null),
    [interval, setInterval] = useState("60"),
    [validMinutes, setValidMinutes] = useState("240"),
    [safety, setSafety] = useState("0"),
    [unreflected, setUnreflected] = useState("0"),
    [bound, setBound] = useState("0"),
    [maximum, setMaximum] = useState("7"),
    [busy, setBusy] = useState(false),
    [message, setMessage] = useState("");
  const [pending, setPending] = useState<string | null>(null);
  const lock = useRef(false);
  useEffect(() => {
    onEnabledChange(enabled);
  }, [enabled, onEnabledChange]);
  useEffect(() => {
    setSelected(null);
    setOpen(false);
    setAck(false);
    setPending(null);
    setMessage("");
  }, [scope.tenant, scope.moysklad_account, scope.ozon_account]);
  if (!data?.available) return null;
  const keyFor = (t: Target) => `${t.ozon_product_id}:${t.ozon_warehouse_id}`;
  const allTargets = data.candidates ?? targets;
  const availableTargets = allTargets.filter((t) => t.eligible !== false);
  const selection = selected ?? availableTargets.slice(0, 1).map(keyFor);
  const selectedTargets = availableTargets.filter((t) => selection.includes(keyFor(t)));
  const current = selectedTargets[0];
  const cycle = data.last_cycle;
  const activeCycle =
    cycle &&
    ["STARTING", "READING", "WAITING_PUBLICATION"].includes(cycle.state);
  const expired =
    !!data.policy && new Date(data.policy.valid_until).getTime() <= Date.now();
  const number = (value: unknown) =>
    value === null || value === undefined ? "нет данных" : String(value);
  const value = (name: string, fallback: unknown) =>
    number(cycle?.metrics?.[name] ?? fallback);
  const latestMarket =
    cycle?.outcome === "RECONCILED"
      ? cycle.metrics.candidate
      : cycle?.metrics?.marketplace_free;
  type TargetResult = {
    outcome?: string;
    source_physical?: string;
    source_reserve?: string;
    marketplace_free?: number;
    candidate?: string;
    readback_quantity?: string;
    publication_id?: string;
    checks?: string[];
  };
  const results = (cycle?.metrics?.target_results || {}) as Record<string, TargetResult>;
  const enrolled: Target[] = (data.policy?.targets || []).map((p) =>
    allTargets.find((t) => keyFor(t) === keyFor(p)) || { ...p, eligible: false },
  );
  const needsAttention = cycle?.state === "BLOCKED" || Number(cycle?.metrics?.blocked_targets || 0) > 0 || enrolled.some((t) => t.eligible === false) || !!data.unresolved?.length;
  const lastSuccess = enrolled
    .filter((t) => t.last_state === "RECONCILED" && t.readback_at)
    .map((t) => t.readback_at!)
    .sort()
    .at(-1);
  function targetOutcome(t: Target): string {
    const result = results[keyFor(t)];
    if (t.eligible === false) return "Требует внимания";
    if (result?.outcome === "NO_CHANGE" || result?.outcome === "RECONCILED") return "Сверено";
    if (result?.outcome === "BLOCKED" || result?.outcome === "PUBLICATION_UNKNOWN" || result?.outcome === "PUBLICATION_REJECTED" || result?.outcome === "RECONCILIATION_PENDING") return "Требует внимания";
    return "Ожидает проверки";
  }
  function targetProblem(checks: string[]): string {
    if (checks.includes("PUBLICATION_UNKNOWN_OR_PENDING")) return "Проверьте результат предыдущей отправки.";
    if (checks.includes("MARKETPLACE_RESERVATION_REQUIRES_REVIEW")) return "Проверьте резерв Ozon для этого товара.";
    if (checks.includes("STOCK_OBSERVATION_MISSING")) return "Нет подтверждённого остатка товара на выбранном складе.";
    if (checks.includes("AUTOMATIC_MAXIMUM_EXCEEDED")) return "Расчёт превышает настроенный предел отправки.";
    return "Проверьте актуальность данных, соответствие товара и настройки.";
  }

  async function send(body: string) {
    if (lock.current) return;
    lock.current = true;
    setBusy(true);
    setMessage("");
    try {
      await post<AutoView>(
        "/api/stock-sync/automatic",
        JSON.parse(body),
        AbortSignal.timeout(30000),
      );
      setPending(null);
      setOpen(false);
      setAck(false);
      await Promise.all([
        client.invalidateQueries({ queryKey: key }),
        client.invalidateQueries({
          queryKey: [
            "stock-sync",
            scope.tenant,
            scope.moysklad_account,
            scope.ozon_account,
          ],
        }),
      ]);
    } catch (error) {
      if (
        error instanceof ApiError &&
        error.status >= 400 &&
        error.status < 500
      ) {
        setPending(null);
        setMessage(
          "Не удалось сохранить настройки. Проверьте выбранный товар, склад и предыдущие отправки.",
        );
      } else {
        setPending(body);
        setMessage(
          "Неизвестно, сохранены ли настройки. Повторите тот же запрос или проверьте состояние; новый запрос не создаётся автоматически.",
        );
      }
      void query.refetch();
    } finally {
      lock.current = false;
      setBusy(false);
    }
  }
  function configure(turnOn: boolean) {
    if (pending || busy) return;
    const body = {
      ...scope,
      enabled: turnOn,
      acknowledge: true,
      idempotency_key: crypto.randomUUID(),
      ...(turnOn && current
        ? {
            interval_seconds: Number(interval),
            valid_minutes: Number(validMinutes),
            ...(selectedTargets.length === 1
              ? {
                  ozon_product_id: current.ozon_product_id,
                  ozon_warehouse_id: current.ozon_warehouse_id,
                  safety_stock: safety,
                  unreflected,
                  unseen_bound: bound,
                  maximum: Number(maximum),
                }
              : {
                  targets: selectedTargets.map((t) => ({
                    ozon_product_id: t.ozon_product_id,
                    ozon_warehouse_id: t.ozon_warehouse_id,
                    safety_stock: safety,
                    unreflected,
                    unseen_bound: bound,
                    maximum: Number(maximum),
                  })),
                }),
          }
        : {}),
    };
    const exact = JSON.stringify(body);
    setPending(exact);
    void send(exact);
  }
  async function runNow() {
    if (busy || pending) return;
    setBusy(true);
    setMessage("");
    try {
      await post(
        "/api/stock-sync/automatic/run",
        { ...scope, acknowledge: true },
        AbortSignal.timeout(30000),
      );
      await query.refetch();
    } catch {
      setMessage(
        "Не удалось запланировать проверку. Обновите состояние и попробуйте снова.",
      );
    } finally {
      setBusy(false);
    }
  }
  async function inspectOperation(id: string) {
    if (busy || pending) return;
    setBusy(true);
    setMessage("");
    try {
      await post(
        `/api/stock-sync/operations/${id}/reconcile`,
        { ...scope, acknowledge: true },
        AbortSignal.timeout(30000),
      );
      await Promise.all([
        query.refetch(),
        client.invalidateQueries({
          queryKey: [
            "stock-sync",
            scope.tenant,
            scope.moysklad_account,
            scope.ozon_account,
          ],
        }),
      ]);
    } catch {
      setMessage(
        "Не удалось уточнить остаток Ozon. Повторная отправка не выполнялась.",
      );
    } finally {
      setBusy(false);
    }
  }
  return (
    <>
      <section
        className="sync-card automatic-stock-card"
        aria-label="Автоматическая синхронизация"
      >
        <div>
          <div className="provider-heading">
            <h2>Автоматическая синхронизация</h2>
            <span
              className={`connection-state ${needsAttention || expired ? "warning" : enabled ? "verified" : ""}`}
            >
              {!enabled
                ? "Выключена"
                : expired
                  ? "Нужна проверка"
                  : activeCycle
                    ? "Проверяем остатки"
                    : needsAttention
                      ? "Требуется внимание"
                      : cycle?.outcome === "NO_CHANGE" ||
                          cycle?.outcome === "RECONCILED"
                        ? "Синхронизировано"
                        : "Включена"}
            </span>
          </div>
          <p>
            {cycle?.outcome
              ? outcomes[cycle.outcome] ||
                "Проверьте данные и настройки синхронизации."
              : enabled
                ? "Остатки проверяются автоматически для выбранной тестовой пары."
                : "Включите автоматическую проверку выбранного товара и склада."}
          </p>
          {cycle && (
            <p>
              МойСклад: {value("source_physical", current?.source_physical)} шт.
              · Резерв: {value("source_reserve", current?.source_reserve)} ·
              Ozon{activeCycle ? " (последняя загрузка)" : ""}:{" "}
              {number(latestMarket ?? current?.marketplace_free)} · Расчётный
              остаток: {value("candidate", current?.quantity)}
            </p>
          )}
          {data.last_cycle_at && (
            <p>
              Последняя проверка:{" "}
              {new Date(data.last_cycle_at).toLocaleString("ru-RU")}.
            </p>
          )}
          {lastSuccess && <p>Последняя сверенная отправка: {new Date(lastSuccess).toLocaleString("ru-RU")}.</p>}
          {enrolled.length > 0 && (
            <details className="technical-details">
              <summary>
                Товаров в настройке: {enrolled.length} · сверено: {enrolled.filter((t) => targetOutcome(t) === "Сверено").length} · ожидают: {enrolled.filter((t) => targetOutcome(t) === "Ожидает проверки").length} · требуют внимания: {enrolled.filter((t) => targetOutcome(t) === "Требует внимания").length}
              </summary>
              {enrolled.map((t) => {
                const r = results[keyFor(t)];
                return (
                  <div key={keyFor(t)}>
                    <p>{t.offer_id || t.ozon_product_id} · склад {t.ozon_warehouse_id} · {targetOutcome(t)}</p>
                    <p>МойСклад: {number(r?.source_physical ?? t.source_physical)} · резерв: {number(r?.source_reserve ?? t.source_reserve)} · Ozon: {number(r?.readback_quantity ?? r?.marketplace_free ?? t.marketplace_free)} · расчёт: {number(r?.candidate)}</p>
                    {t.eligible === false ? <p>Нет актуального товара или остатка на поддерживаемом складе Ozon.</p> : r?.checks?.length ? <p>{targetProblem(r.checks)}</p> : null}
                    {r?.publication_id && <p>Последняя отправка: {r.publication_id}</p>}
                    {t.readback_at && <p>Последняя проверка Ozon: {new Date(t.readback_at).toLocaleString("ru-RU")}.</p>}
                  </div>
                );
              })}
            </details>
          )}
          {!!data.unresolved?.length && (
            <details className="technical-details">
              <summary>
                Отправки, требующие проверки ({data.unresolved.length})
              </summary>
              {data.unresolved.map((item) => (
                <div key={item.operation_id}>
                  <p>
                    Товар {item.ozon_product_id} · склад{" "}
                    {item.ozon_warehouse_id} · запрос {item.quantity} шт.
                  </p>
                  <p>
                    {item.state === "UNKNOWN"
                      ? "Ответ Ozon не подтверждён; повторная отправка заблокирована."
                      : "Ozon ответил, но остаток ещё проверяется."}{" "}
                    {item.dispatched_at &&
                      `Попытка: ${new Date(item.dispatched_at).toLocaleString("ru-RU")}.`}
                  </p>
                  <p>
                    Последний остаток: {item.readback_quantity ?? "нет данных"}
                    {item.readback_at &&
                      ` · ${new Date(item.readback_at).toLocaleString("ru-RU")}`}
                    .
                  </p>
                  <button
                    className="text-action"
                    disabled={busy || !!pending}
                    onClick={() => void inspectOperation(item.operation_id)}
                  >
                    Уточнить этот результат
                  </button>
                  {item.state === "UNKNOWN" && item.readback_at && item.readback_quantity !== undefined && onRecover && (
                    <button className="text-action" disabled={busy || !!pending} onClick={() => onRecover(item)}>
                      Рассмотреть восстановление
                    </button>
                  )}
                </div>
              ))}
            </details>
          )}
          {enabled && data.next_run_at && (
            <p>
              Следующая проверка:{" "}
              {new Date(data.next_run_at).toLocaleString("ru-RU")}.
            </p>
          )}
          {enabled && data.policy && (
            <p>
              Тестовые допущения действуют до{" "}
              {new Date(data.policy.valid_until).toLocaleString("ru-RU")}.
            </p>
          )}
          {message && (
            <p className="is-error" role="alert">
              {message}
            </p>
          )}
          {pending && !busy && (
            <button onClick={() => void send(pending)}>
              Повторить тот же запрос
            </button>
          )}
          {query.error && (
            <p className="is-error" role="alert">
              Не удалось загрузить настройки автоматической синхронизации.
            </p>
          )}
        </div>
        <div className="sync-actions">
          {enabled ? (
            <>
              <button
                disabled={busy || !!pending || !!activeCycle || expired}
                onClick={() => void runNow()}
              >
                Проверить сейчас
              </button>
              <button
                className="secondary"
                disabled={busy || !!pending}
                onClick={() => configure(false)}
              >
                Остановить автоматическую проверку
              </button>
              <button
                disabled={busy || !!pending || !!activeCycle}
                onClick={() => {
                  const policy = data.policy,
                    target = policy?.targets[0];
                  if (policy) setInterval(String(policy.interval_seconds));
                  if (target) {
                    setSelected(
                      policy?.targets.map(
                        (t) => `${t.ozon_product_id}:${t.ozon_warehouse_id}`,
                      ) || [],
                    );
                    setSafety(target.safety_stock);
                    setUnreflected(target.unreflected);
                    setBound(target.unseen_bound);
                    setMaximum(String(target.maximum));
                  }
                  setAck(false);
                  setOpen(true);
                }}
              >
                Изменить настройки
              </button>
            </>
          ) : (
            <button
              disabled={busy || !!pending || !availableTargets.length}
              onClick={() => setOpen(true)}
            >
              Настроить автоматическую синхронизацию
            </button>
          )}
        </div>
      </section>
      {open && (
        <IntegrationDialog
          title="Настроить автоматическую синхронизацию?"
          onClose={() => {
            if (!busy && !pending) setOpen(false);
          }}
        >
          <div className="provider-settings-body">
            <p>
              Для выбранных тестовых аккаунтов STYK будет автоматически читать
              остатки и отправлять изменения в Ozon. Положительный остаток может
              быть доступен реальным покупателям.
            </p>
            {availableTargets.length > 1 ? (
              <fieldset className="integration-field">
                <legend>Товары и склады для синхронизации</legend>
                {availableTargets.map((t) => (
                  <label className="integration-ack" key={keyFor(t)}>
                    <input
                      type="checkbox"
                      checked={selection.includes(keyFor(t))}
                      onChange={(e) =>
                        setSelected(
                          e.target.checked
                            ? [...selection, keyFor(t)]
                            : selection.filter((v) => v !== keyFor(t)),
                        )
                      }
                    />
                    Товар {t.offer_id || t.ozon_product_id}, склад {t.ozon_warehouse_id}
                  </label>
                ))}
              </fieldset>
            ) : (
              <label className="integration-field">
                Товар и склад
                <select
                  value={current ? keyFor(current) : ""}
                  onChange={(e) => setSelected([e.target.value])}
                >
                  {availableTargets.map((t) => (
                    <option key={keyFor(t)} value={keyFor(t)}>
                      Товар {t.offer_id || t.ozon_product_id}, склад {t.ozon_warehouse_id}
                    </option>
                  ))}
                </select>
              </label>
            )}
            {selectedTargets.length > 1 && (
              <p>
                Указанные ниже количества и предел применяются к каждой
                выбранной паре.
              </p>
            )}
            <label className="integration-field">
              Интервал проверки, секунд
              <input
                type="number"
                min="30"
                max="3600"
                value={interval}
                onChange={(e) => setInterval(e.target.value)}
              />
            </label>
            <label className="integration-field">
              Срок допущений, минут
              <input
                type="number"
                min="1"
                max="1440"
                value={validMinutes}
                onChange={(e) => setValidMinutes(e.target.value)}
              />
            </label>
            <label className="integration-field">
              Страховой остаток
              <input
                type="number"
                min="0"
                value={safety}
                onChange={(e) => setSafety(e.target.value)}
              />
            </label>
            <label className="integration-field">
              Неучтённые обязательства
              <input
                type="number"
                min="0"
                value={unreflected}
                onChange={(e) => setUnreflected(e.target.value)}
              />
            </label>
            <label className="integration-field">
              Предел неизвестного уменьшения
              <input
                type="number"
                min="0"
                value={bound}
                onChange={(e) => setBound(e.target.value)}
              />
            </label>
            <label className="integration-field">
              Максимум к отправке
              <input
                type="number"
                min="0"
                max="10"
                value={maximum}
                onChange={(e) => setMaximum(e.target.value)}
              />
            </label>
            <p>
              Предполагается одна единица товара на одну единицу продажи и
              отсутствие других каналов, которые расходуют тот же остаток.
              Покрытие заказов и резервов независимо не подтверждено. Эти
              условия будут сохранены как утверждения оператора, а не данные
              провайдера.
            </p>
            <label className="integration-ack">
              <input
                type="checkbox"
                checked={ack}
                onChange={(e) => setAck(e.target.checked)}
              />
              Подтверждаю выбранные настройки и принимаю риск автоматической
              отправки для тестовых аккаунтов.
            </label>
            <div className="provider-actions">
              <button
                disabled={!ack || !current || busy || !!pending}
                onClick={() => configure(true)}
              >
                Включить автоматическую проверку
              </button>
              <button
                className="secondary"
                disabled={busy || !!pending}
                onClick={() => setOpen(false)}
              >
                Отмена
              </button>
            </div>
            {message && (
              <p className="is-error" role="alert">
                {message}
              </p>
            )}
            {pending && !busy && (
              <button onClick={() => void send(pending)}>
                Повторить тот же запрос
              </button>
            )}
          </div>
        </IntegrationDialog>
      )}
    </>
  );
}
