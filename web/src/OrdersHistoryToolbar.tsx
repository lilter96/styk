import { useId } from "react";
import { Icon } from "./UI";
import {
  historyResults,
  type HistoryFilters,
} from "./OrdersHistoryPresentation";

export function OrdersHistoryToolbar({
  filters,
  onChange,
}: {
  filters: HistoryFilters;
  onChange: (filters: HistoryFilters) => void;
}) {
  const help = useId();
  return (
    <div className="history-toolbar">
      <form role="search" onSubmit={(event) => event.preventDefault()}>
        <label className="orders-search">
          <Icon name="search" size="button" />
          <span className="sr-only">Номер отправления в истории</span>
          <input
            type="search"
            value={filters.search}
            placeholder="Номер отправления"
            aria-describedby={`${help}-search`}
            onChange={(event) =>
              onChange({ ...filters, search: event.target.value })
            }
          />
        </label>
        <span id={`${help}-search`} className="sr-only">
          Поиск по точному номеру отправления во всей истории
        </span>
      </form>
      <label>
        <span className="sr-only">Результат операции</span>
        <select
          value={filters.status}
          onChange={(event) =>
            onChange({ ...filters, status: event.target.value })
          }
        >
          <option value="">Все результаты</option>
          {Object.entries(historyResults).map(([state, result]) => (
            <option key={state} value={state}>
              {result.label}
            </option>
          ))}
        </select>
      </label>
      <div
        className="history-segments"
        role="group"
        aria-label="Внимание к операциям"
      >
        <button
          type="button"
          className="history-segment"
          aria-pressed={!filters.attention}
          onClick={() => onChange({ ...filters, attention: false })}
        >
          Все
        </button>
        <button
          type="button"
          className="history-segment"
          aria-pressed={filters.attention}
          onClick={() => onChange({ ...filters, attention: true })}
        >
          Нужна проверка
        </button>
      </div>
      <details className="history-help">
        <summary>
          <Icon name="error" size="button" />
          Как понимать статусы?
        </summary>
        <div>
          <p>«Готово» — результат подтверждён сверкой.</p>
          <p>Ожидание подтверждения ещё не означает завершение.</p>
          <p>
            «Нужна проверка» — исход неизвестен. «Отклонено» и «Действие
            заблокировано» тоже требуют внимания.
          </p>
        </div>
      </details>
    </div>
  );
}
