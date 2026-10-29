import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "./api";
import {
  MoyOrderLink,
  orderActionLabel,
  orderDate,
  orderRequestError,
  ordersURL,
  orderStateLabels,
  sameOrderScope,
  type OrderScope,
  type OrdersOperationPage,
} from "./OrdersTypes";

export function OrdersOperations({
  scope,
  onLinks,
  onOutcomes,
}: {
  scope: OrderScope;
  onLinks?: (links: Record<string, string>) => void;
  onOutcomes?: (states: Record<string, string>) => void;
}) {
  const [cursors, setCursors] = useState([""]);
  const cursor = cursors[cursors.length - 1];
  const history = useQuery({
    queryKey: ["orders-operations", scope, cursor],
    queryFn: async ({ signal }) => {
      const value = await api<OrdersOperationPage>(
        ordersURL("operations", scope, { cursor, limit: "25" }),
        signal,
      );
      if (
        (value.operations ?? []).some(
          (item) => !sameOrderScope(item.scope, scope),
        )
      )
        throw new Error("operation scope mismatch");
      return value;
    },
    retry: false,
    refetchOnWindowFocus: false,
    refetchInterval: (query) =>
      (query.state.data?.operations ?? []).some((operation) =>
        (operation.targets ?? []).some((work) =>
          [
            "QUEUED",
            "CLAIMED",
            "DISPATCHING",
            "ACKNOWLEDGED",
            "POLLING",
            "UNKNOWN",
          ].includes(work.state),
        ),
      )
        ? 10000
        : false,
  });
  useEffect(() => {
    const links: Record<string, string> = {};
    const states: Record<string, string> = {};
    for (const operation of history.data?.operations ?? [])
      for (const work of operation.targets ?? []) {
        const id =
          work.command.correlation?.customer_order_id ||
          work.receipt?.external_id;
        if (id) links[work.command.posting_number] = id;
        states[work.command.posting_number] ??= work.state;
      }
    onLinks?.(links);
    onOutcomes?.(states);
  }, [history.data, onLinks, onOutcomes]);
  return (
    <section
      className="orders-section"
      aria-labelledby="orders-history-heading"
    >
      <div className="orders-section-heading">
        <h2 id="orders-history-heading">История передачи</h2>
        <button
          type="button"
          className="secondary"
          disabled={history.isFetching}
          onClick={() => void history.refetch()}
        >
          Обновить статусы заказов
        </button>
      </div>
      <p className="orders-note">
        Принятие запроса ещё не подтверждает изменения в МойСклад. Итог —
        «Подтверждено сверкой» для каждого отправления.
      </p>
      {history.isLoading && <p role="status">Читаем историю…</p>}
      {history.isError && (
        <p role="alert" className="orders-error">
          {orderRequestError(history.error)}
        </p>
      )}
      {!history.isLoading &&
        !history.isError &&
        !history.data?.operations?.length && (
          <p className="orders-empty">
            Передач пока нет. Выберите отправления и проверьте изменения перед
            подтверждением.
          </p>
        )}
      {(history.data?.operations ?? []).map((operation) => (
        <article className="orders-posting" key={operation.id}>
          <div className="orders-section-heading">
            <h3>Передача от {orderDate(operation.created_at)}</h3>
            <span className="orders-badge">
              {orderStateLabels[operation.state] ?? operation.state}
            </span>
          </div>
          {(operation.targets ?? []).map((work) => (
            <div className="orders-work" key={work.id}>
              <div>
                <strong>{work.command.posting_number}</strong>
                <span
                  className={`orders-badge ${work.state === "UNKNOWN" || work.state === "BLOCKED" ? "warning" : work.state === "RECONCILED" ? "success" : ""}`}
                >
                  {orderStateLabels[work.state] ?? work.state}
                </span>
              </div>
              <p>
                {orderActionLabel(work.command.action)} ·{" "}
                {work.automatic
                  ? "По разрешению автоматизации"
                  : "Подтверждено вручную"}
              </p>
              <MoyOrderLink
                id={
                  work.command.correlation?.customer_order_id ||
                  work.receipt?.external_id
                }
              />
              {work.state === "UNKNOWN" && (
                <p className="orders-warning">
                  Запрос мог выполниться. STYK сверяет результат чтением;
                  повторная отправка автоматически не выполняется. Обновите
                  статусы и дождитесь подтверждения.
                </p>
              )}
              {work.state === "BLOCKED" && (
                <p className="orders-warning">
                  Условия передачи изменились. Проверьте настройки и связи,
                  затем создайте новый предпросмотр.
                </p>
              )}
              {(work.code || work.receipt?.code) && (
                <details>
                  <summary>Сведения об операции</summary>
                  <p>
                    Код: <code>{work.code || work.receipt.code}</code>
                  </p>
                  <p>ID: {work.id}</p>
                </details>
              )}
            </div>
          ))}
        </article>
      ))}
      <div className="orders-actions">
        <button
          className="secondary"
          disabled={history.isFetching || cursors.length === 1}
          onClick={() => setCursors((value) => value.slice(0, -1))}
        >
          Предыдущие операции
        </button>
        <span>Страница {cursors.length}</span>
        <button
          className="secondary"
          disabled={history.isFetching || !history.data?.next_cursor}
          onClick={() =>
            setCursors((value) => [...value, history.data!.next_cursor!])
          }
        >
          Следующие операции
        </button>
      </div>
    </section>
  );
}
