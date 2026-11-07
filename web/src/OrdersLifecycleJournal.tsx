import { useQuery } from "@tanstack/react-query";
import { api } from "./api";
import {
  orderActionLabel,
  orderDate,
  orderIssueLabel,
  orderRequestError,
  ordersURL,
  sameOrderScope,
  type NativeScope,
  type OrderScope,
} from "./OrdersTypes";

type IntentStatus = {
  intent: {
    id: string;
    scope: NativeScope;
    correlation: { posting_number: string; customer_order_id: string };
    action: string;
    observed_at: string;
    before_state_id: string;
    after_state_id: string;
  };
  state: string;
  code?: string;
  work_id?: string;
};

export function OrdersLifecycleJournal({ scope }: { scope: OrderScope }) {
  const journal = useQuery({
    queryKey: ["orders-merchant-intents", scope],
    queryFn: async ({ signal }) => {
      const value = await api<{ intents: IntentStatus[] }>(
        ordersURL("merchant-intents", scope, { limit: "25" }),
        signal,
      );
      if (
        !Array.isArray(value.intents) ||
        value.intents.some((item) => !sameOrderScope(item.intent?.scope, scope))
      )
        throw new Error("merchant intent scope mismatch");
      return value.intents;
    },
    refetchInterval: 15000,
    retry: false,
  });
  return (
    <div className="settings-card settings-lifecycle-journal">
      <div className="settings-card-title">
        <h3>Последние команды из МойСклад</h3>
        <button
          type="button"
          className="text-action"
          disabled={journal.isFetching}
          onClick={() => void journal.refetch()}
        >
          Обновить команды
        </button>
      </div>
      <p>
        Последние 25 обнаруженных переходов. Постановка в очередь ещё не
        подтверждает результат Ozon или складского документа: проверяйте историю
        передач.
      </p>
      {journal.isPending && (
        <p role="status">Загружаем обнаруженные переходы…</p>
      )}
      {journal.isError && (
        <p role="alert">{orderRequestError(journal.error)}</p>
      )}
      {journal.data?.length === 0 && (
        <p>
          Переходов пока нет. После включения разрешения STYK сначала запоминает
          исходное состояние, затем проверяет новые изменения продавца.
        </p>
      )}
      {journal.data && journal.data.length > 0 && (
        <ul className="settings-lifecycle-events">
          {journal.data.map((item) => (
            <li key={item.intent.id}>
              <strong>{item.intent.correlation.posting_number}</strong>
              <span>{orderActionLabel(item.intent.action)}</span>
              <span>
                {item.state === "OBSERVED"
                  ? "Ожидает проверки"
                  : item.state === "QUEUED"
                    ? "Передано в очередь; результат в истории"
                    : item.state === "BLOCKED"
                      ? "Требуется вмешательство"
                      : "Состояние требует проверки"}
              </span>
              <time dateTime={item.intent.observed_at}>
                {orderDate(item.intent.observed_at)}
              </time>
              {item.state === "BLOCKED" && (
                <p>
                  {orderIssueLabel(
                    item.code ?? "MERCHANT_ACTION_REQUIRES_EVIDENCE",
                  )}
                </p>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
