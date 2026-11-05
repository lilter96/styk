import { Icon } from "./UI";
import { OrdersHistoryStatus } from "./OrdersHistoryStatus";
import {
  historyActionTitle,
  historyGroups,
  historyInitiator,
  historyNeedsAttention,
  historyTime,
  type HistoryRow,
} from "./OrdersHistoryPresentation";
import { orderDate } from "./OrdersTypes";

export function OrdersHistoryTable({
  rows,
  onOpen,
}: {
  rows: HistoryRow[];
  onOpen: (id: string) => void;
}) {
  return (
    <div className="history-table-scroll">
      <table className="history-table" aria-label="История действий с заказами">
        <thead>
          <tr>
            {[
              "Когда",
              "Отправление",
              "Что произошло",
              "Результат",
              "Действие",
            ].map((title) => (
              <th key={title} scope="col">
                {title}
              </th>
            ))}
          </tr>
        </thead>
        {historyGroups(rows).map((group) => (
          <tbody key={group.key}>
            <tr className="history-date-group">
              <th colSpan={5} scope="rowgroup">
                {group.label}
              </th>
            </tr>
            {group.rows.map((row) => (
              <HistoryTableRow
                key={row.id}
                row={row}
                onOpen={() => onOpen(row.id)}
              />
            ))}
          </tbody>
        ))}
      </table>
    </div>
  );
}
function HistoryTableRow({
  row,
  onOpen,
}: {
  row: HistoryRow;
  onOpen: () => void;
}) {
  const { work, operation } = row;
  const number = work?.command.posting_number;
  const title = historyActionTitle(work?.command.action, work?.state);
  const attention = !!work && historyNeedsAttention(work.state);
  return (
    <tr
      className={attention ? "history-row needs-attention" : "history-row"}
      onClick={(event) => {
        if ((event.target as HTMLElement).closest("button, a")) return;
        event.currentTarget.querySelector("button")?.focus();
        onOpen();
      }}
    >
      <td data-label="Когда">
        <time
          title={orderDate(operation.created_at)}
          dateTime={
            Number.isFinite(Date.parse(operation.created_at))
              ? operation.created_at
              : undefined
          }
        >
          {historyTime(operation.created_at)}
        </time>
      </td>
      <td data-label="Отправление">
        <strong>{number || "Без отправления"}</strong>
        {work?.command.commercial_order_id && (
          <small>Заказ {work.command.commercial_order_id}</small>
        )}
      </td>
      <td data-label="Что произошло">
        <strong>{title}</strong>
        <small>{historyInitiator(work)}</small>
      </td>
      <td data-label="Результат">
        <OrdersHistoryStatus state={work?.state} />
      </td>
      <td data-label="Действие">
        <button
          type="button"
          className={
            attention ? "secondary history-open attention" : "history-open"
          }
          aria-label={`${attention ? "Разобраться" : "Подробнее"}: ${number || "без отправления"}, ${title}`}
          onClick={onOpen}
        >
          {attention ? "Разобраться" : "Подробнее"}
          <Icon name="chevron-right" size="button" />
        </button>
      </td>
    </tr>
  );
}
