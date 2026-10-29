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
};
type Refresh = { source_status: string; posting_status: string };
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
    [hours, setHours] = useState("1"),
    [busy, setBusy] = useState(false),
    [message, setMessage] = useState("");
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
  async function refresh() {
    if (busy) return;
    setBusy(true);
    setMessage("");
    try {
      const result = await post<Refresh>(
        "/api/stock-sync/order-evidence/refresh",
        { ...scope, window_hours: Number(hours) },
        AbortSignal.timeout(60000),
      );
      setMessage(
        result.source_status === "COMPLETED" &&
          result.posting_status === "COMPLETED"
          ? "Проверка обоих сервисов завершена."
          : result.source_status === "LIMIT_REACHED" ||
              result.posting_status === "LIMIT_REACHED"
            ? "Период слишком большой для одной проверки. Выберите меньший период; неполные данные не сохранены."
            : "Не все данные удалось загрузить. Показаны только завершённые проверки.",
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
        "Не удалось завершить проверку. Повторной отправки остатков не было.",
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
            value={hours}
            disabled={busy}
            onChange={(event) => setHours(event.target.value)}
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
          {busy ? "Проверяем заказы…" : "Проверить заказы"}
        </button>
      </div>
      {message && <p role="status">{message}</p>}
    </details>
  );
}
