import { useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError, params, post } from "./api";
import { IntegrationDialog } from "./IntegrationDialog";

type Scope = { tenant: string; moysklad_account: string; ozon_account: string };
type Target = {
  ozon_product_id: string;
  ozon_warehouse_id: string;
  source_physical?: string;
  source_reserve?: string;
  marketplace_free?: number;
  quantity?: string;
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
  last_cycle?: {
    id: string;
    state: string;
    outcome?: string;
    started_at: string;
    finished_at?: string;
    publication_id?: string;
    metrics: Record<string, unknown>;
  };
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
};

export function AutomaticStockControls({
  scope,
  targets,
  onEnabledChange,
}: {
  scope: Scope;
  targets: Target[];
  onEnabledChange: (enabled: boolean) => void;
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
    [selected, setSelected] = useState(""),
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
    setSelected("");
    setOpen(false);
    setAck(false);
    setPending(null);
    setMessage("");
  }, [scope.tenant, scope.moysklad_account, scope.ozon_account]);
  if (!data?.available) return null;
  const current =
    targets.find(
      (t) => `${t.ozon_product_id}:${t.ozon_warehouse_id}` === selected,
    ) || targets[0];
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
            ozon_product_id: current.ozon_product_id,
            ozon_warehouse_id: current.ozon_warehouse_id,
            safety_stock: safety,
            unreflected,
            unseen_bound: bound,
            maximum: Number(maximum),
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
  return (
    <>
      <section className="sync-card" aria-label="Автоматическая синхронизация">
        <div>
          <div className="provider-heading">
            <h2>Автоматическая синхронизация</h2>
            <span
              className={`connection-state ${cycle?.state === "BLOCKED" || expired ? "warning" : enabled ? "verified" : ""}`}
            >
              {!enabled
                ? "Выключена"
                : expired
                  ? "Нужна проверка"
                  : activeCycle
                    ? "Проверяем остатки"
                    : cycle?.state === "BLOCKED"
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
              Ozon: {number(latestMarket ?? current?.marketplace_free)} ·
              Расчётный остаток: {value("candidate", current?.quantity)}
            </p>
          )}
          {data.last_cycle_at && (
            <p>
              Последняя проверка:{" "}
              {new Date(data.last_cycle_at).toLocaleString("ru-RU")}.
            </p>
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
                      `${target.ozon_product_id}:${target.ozon_warehouse_id}`,
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
              disabled={busy || !!pending || !targets.length}
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
            <label className="integration-field">
              Товар и склад
              <select
                value={
                  selected ||
                  (current
                    ? `${current.ozon_product_id}:${current.ozon_warehouse_id}`
                    : "")
                }
                onChange={(e) => setSelected(e.target.value)}
              >
                {targets.map((t) => (
                  <option
                    key={`${t.ozon_product_id}:${t.ozon_warehouse_id}`}
                    value={`${t.ozon_product_id}:${t.ozon_warehouse_id}`}
                  >
                    Товар {t.ozon_product_id}, склад {t.ozon_warehouse_id}
                  </option>
                ))}
              </select>
            </label>
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
                max="7"
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
