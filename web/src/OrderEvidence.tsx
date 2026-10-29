import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, params, post } from "./api";

type Scope = { tenant: string; moysklad_account: string; ozon_account: string };
type Evidence = {
  source_batch_id?: number;
  source_from?: string;
  source_to?: string;
  source_checked_at?: string;
  positions:
    | {
        order_id: string;
        position_id: string;
        warehouse_id?: string;
        quantity: string | null;
        reserve: string | null;
        shipped: string | null;
        applicable?: boolean;
        source_updated?: string;
        observed_at: string;
      }[]
    | null;
  posting_batch_id?: number;
  posting_from?: string;
  posting_to?: string;
  postings:
    | {
        posting_number: string;
        status: string;
        warehouse_id: string;
        quantity: number;
        observed_at: string;
      }[]
    | null;
  truncated: boolean;
  progress?: Progress | null;
};
type Refresh = {
  source_status: string;
  posting_status: string;
  source_completed_windows?: number;
  source_pending_windows?: number;
  source_limit_resource?: string;
  posting_completed_windows?: number;
  posting_pending_windows?: number;
  posting_limit_resource?: string;
};
type Progress = Refresh & {
  window_hours: number;
  from: string;
  to: string;
  updated_at: string;
  credential_changed?: boolean;
  expired?: boolean;
  expires_at?: string;
};
type Run = { scopeKey: string; hours: string; result: Refresh };
const statusText: Record<string, string> = {
  COMPLETED: "период проверен полностью",
  IN_PROGRESS: "проверка не завершена",
  FAILED: "проверка не завершилась из-за ошибки",
  LIMIT_REACHED: "достигнут предел проверки; выберите меньший период",
  RATE_LIMITED: "сервис ограничил частоту запросов",
  AUTHORIZATION_REQUIRED: "требуется проверить подключение",
  UNAVAILABLE: "сервис временно недоступен",
  INVALID_RESPONSE: "получен некорректный ответ сервиса",
  CONFLICT: "проверку не удалось продолжить из-за конфликта",
};
const limitText: Record<string, string> = {
  requests: "число запросов",
  attempts: "число попыток",
  pages: "число страниц",
  response_bytes: "размер ответа",
  postings: "число отправлений",
  observations: "число наблюдений",
  duration: "время проверки",
  staging_bytes: "объём временных данных",
};
function progressText(
  name: string,
  status: string,
  completed?: number,
  pending?: number,
  resource?: string,
) {
  const counts =
    typeof completed === "number" && typeof pending === "number"
      ? ` Завершено интервалов: ${completed}; осталось: ${pending}.`
      : "";
  const limit = resource
    ? ` Ограничение: ${limitText[resource] || "ресурс проверки"}.`
    : "";
  return `${name}: ${statusText[status] || "состояние проверки неизвестно"}.${counts}${limit}`;
}
const date = (value?: string) =>
  value ? new Date(value).toLocaleString("ru-RU") : "нет данных";
const quantity = (value: string | null) => value ?? "неизвестно";
const postingStatus: Record<string, string> = {
  awaiting_packaging: "ожидает упаковки",
  awaiting_deliver: "ожидает передачи",
  delivering: "в доставке",
  delivered: "доставлено",
  cancelled: "отменено",
};

export function OrderEvidence({
  scope,
  product,
  warehouse,
}: {
  scope: Scope;
  product: string;
  warehouse: string;
}) {
  const [open, setOpen] = useState(false),
    [hours, setHours] = useState<{ scopeKey: string; value: string } | null>(
      null,
    ),
    [busy, setBusy] = useState(false),
    [message, setMessage] = useState(""),
    [run, setRun] = useState<Run | null>(null);
  const client = useQueryClient();
  const key = [
    "order-evidence",
    scope.tenant,
    scope.moysklad_account,
    scope.ozon_account,
    product,
    warehouse,
  ];
  const query = useQuery({
    queryKey: key,
    queryFn: ({ signal }) =>
      api<Evidence>(
        `/api/stock-sync/order-evidence?${params({ ...scope, product, warehouse })}`,
        signal,
      ),
    enabled: open,
    retry: false,
  });
  const scopeKey = JSON.stringify(scope);
  const persisted = query.data?.progress;
  const savedRun = persisted
    ? { scopeKey, hours: String(persisted.window_hours), result: persisted }
    : null;
  const currentRun =
    persisted?.credential_changed || persisted?.expired
      ? savedRun
      : run?.scopeKey === scopeKey
        ? run
        : savedRun;
  const selectedHours =
    (hours?.scopeKey === scopeKey ? hours.value : null) ??
    currentRun?.hours ??
    "1";
  const resumable =
    currentRun &&
    [currentRun.result.source_status, currentRun.result.posting_status].some(
      (status) => status !== "COMPLETED" && status !== "LIMIT_REACHED",
    );
  const credentialChanged = !!persisted?.credential_changed;
  const expired = !!persisted?.expired;
  const continuing =
    !!resumable &&
    currentRun.hours === selectedHours &&
    !credentialChanged &&
    !expired;
  async function refresh() {
    if (busy) return;
    setBusy(true);
    setMessage("");
    try {
      const result = await post<Refresh>(
        "/api/stock-sync/order-evidence/refresh",
        { ...scope, window_hours: Number(selectedHours) },
        AbortSignal.timeout(60000),
      );
      setRun({ scopeKey, hours: selectedHours, result });
      setMessage(
        result.source_status === "COMPLETED" &&
          result.posting_status === "COMPLETED"
          ? "Проверка обоих сервисов за выбранный период завершена."
          : "Проверка завершена не для всех сервисов. Неполные данные не сохранены как завершённая проверка; ниже показаны только последние завершённые проверки.",
      );
      await client.invalidateQueries({
        queryKey: [
          "order-evidence",
          scope.tenant,
          scope.moysklad_account,
          scope.ozon_account,
        ],
      });
    } catch {
      setMessage(
        "Не удалось получить результат проверки. Обновите сведения или повторите проверку; отправки остатков не было.",
      );
    } finally {
      setBusy(false);
    }
  }
  return (
    <details
      className="technical-details"
      onToggle={(event) => setOpen(event.currentTarget.open)}
    >
      <summary>Заказы и резервы</summary>
      <p>
        Проверка охватывает ограниченный период. Отсутствие заказа в результатах
        не доказывает отсутствие обязательств; резерв заказа может уже входить в
        резерв МойСклад.
      </p>
      {query.isLoading && <p role="status">Загружаем сведения…</p>}
      {query.isError && (
        <p role="alert">
          Не удалось показать сведения для этой пары товара и склада.
        </p>
      )}
      {query.data && (
        <>
          <p>
            МойСклад:{" "}
            {query.data.source_batch_id
              ? `проверено ${date(query.data.source_checked_at)}; изменения с ${date(query.data.source_from)} по ${date(query.data.source_to)}`
              : "проверка заказов ещё не загружена"}
            .
          </p>
          {(query.data.positions || []).map((p) => (
            <p key={`${p.order_id}:${p.position_id}`}>
              Заказ {p.order_id}: заказано {quantity(p.quantity)}, резерв{" "}
              {quantity(p.reserve)}, отгружено {quantity(p.shipped)};{" "}
              {p.applicable === true
                ? "проведён"
                : p.applicable === false
                  ? "не проведён"
                  : "статус проведения неизвестен"}
              {p.warehouse_id ? "" : "; склад не указан"}. Наблюдение:{" "}
              {date(p.observed_at)}.
            </p>
          ))}
          <p>
            Ozon:{" "}
            {query.data.posting_batch_id
              ? `отправления за период с ${date(query.data.posting_from)} по ${date(query.data.posting_to)}`
              : "проверка отправлений ещё не загружена"}
            .
          </p>
          {(query.data.postings || []).map((p) => (
            <p key={`${p.posting_number}:${p.warehouse_id}`}>
              Отправление {p.posting_number}: {p.quantity} шт.; состояние{" "}
              {postingStatus[p.status] || "неизвестно"};{" "}
              {p.warehouse_id ? `склад ${p.warehouse_id}` : "склад неизвестен"}.
              Наблюдение: {date(p.observed_at)}.
            </p>
          ))}
          {query.data.truncated && (
            <p role="alert">
              Показана только часть записей. Полнота данных не установлена.
            </p>
          )}
        </>
      )}
      <div className="order-evidence-actions">
        <label>
          Период проверки
          <select
            value={selectedHours}
            disabled={busy}
            onChange={(event) =>
              setHours({ scopeKey, value: event.target.value })
            }
          >
            <option value="1">Последний час</option>
            <option value="24">Последние сутки</option>
            <option value="168">Последние 7 дней</option>
          </select>
        </label>
        <button
          className="secondary"
          disabled={busy}
          onClick={() => void refresh()}
        >
          {busy
            ? "Проверяем заказы…"
            : continuing
              ? "Продолжить проверку"
              : resumable
                ? "Начать новую проверку"
                : "Проверить заказы"}
        </button>
      </div>
      {currentRun && (
        <div role="status">
          <p>
            {progressText(
              "МойСклад",
              currentRun.result.source_status,
              currentRun.result.source_completed_windows,
              currentRun.result.source_pending_windows,
            )}
          </p>
          <p>
            {progressText(
              "Ozon",
              currentRun.result.posting_status,
              currentRun.result.posting_completed_windows,
              currentRun.result.posting_pending_windows,
            )}
          </p>
          {(currentRun.result.source_status !== "COMPLETED" ||
            currentRun.result.posting_status !== "COMPLETED") && (
            <p>
              Показаны только последние завершённые проверки; неполная проверка
              не подтверждает полноту данных.
            </p>
          )}
          {persisted &&
            (run?.scopeKey !== scopeKey || credentialChanged || expired) && (
              <p>
                Период проверки: с {date(persisted.from)} по{" "}
                {date(persisted.to)}.
              </p>
            )}
          {resumable && selectedHours !== currentRun.hours && (
            <p>
              Выбран другой период: следующий запрос начнёт новую проверку и
              заменит незавершённый прогресс.
            </p>
          )}
          {credentialChanged && (
            <p>
              Подключение изменилось. Следующий запрос начнёт проверку заново.
            </p>
          )}
          {expired && (
            <p>
              Срок незавершённой проверки истёк. Следующий запрос начнёт
              проверку заново.
            </p>
          )}
        </div>
      )}
      {message && <p role="status">{message}</p>}
    </details>
  );
}
