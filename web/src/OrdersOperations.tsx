import { useEffect, useState } from "react";
import { OrdersPostingHistory } from "./OrdersPostingHistory";
import { useOrdersHistory } from "./useOrdersHistory";
import { useLabelExpiryClock } from "./useLabelExpiryClock";
import { OrdersHistoryToolbar } from "./OrdersHistoryToolbar";
import { OrdersHistoryTable } from "./OrdersHistoryTable";
import { OrderHistoryDetailDrawer } from "./OrderHistoryDetailDrawer";
import { historyRows, type HistoryFilters } from "./OrdersHistoryPresentation";
import { Icon } from "./UI";
import { orderRequestError, type OrderScope } from "./OrdersTypes";
import "./OrdersHistory.css";

type Props = {
  scope: OrderScope;
  postingNumber?: string;
  onLinks?: (links: Record<string, string>) => void;
  onOutcomes?: (states: Record<string, string>) => void;
  onRecoveryAuthorization?: (
    posting: string,
    until: string | undefined,
  ) => void;
};
const emptyFilters: HistoryFilters = {
  search: "",
  status: "",
  attention: false,
};
export function OrdersOperations(props: Props) {
  if (props.postingNumber) return <OrdersPostingHistory {...props} />;
  return <OrdersHistory key={JSON.stringify(props.scope)} {...props} />;
}
function OrdersHistory(props: Props) {
  const [filters, setFilters] = useState(emptyFilters);
  const [search, setSearch] = useState("");
  const [cursors, setCursors] = useState([""]);
  const [opened, setOpened] = useState<string>();
  const { latest, page } = useOrdersHistory(
    props.scope,
    cursors.at(-1)!,
    search,
    filters.status,
    filters.attention,
  );
  useLatestOutcomes(latest.data, props.onLinks, props.onOutcomes);
  const now = useLabelExpiryClock(page.data);
  const applied = { ...filters, search };
  const rows = historyRows(page.data?.operations ?? [], applied);
  const selected = rows.find((row) => row.id === opened);
  const selectedID = selected?.id;
  useEffect(() => {
    if (opened && !selectedID && !page.isLoading) setOpened(undefined);
  }, [opened, selectedID, page.isLoading]);
  useEffect(() => {
    if (filters.search.trim() === search) return;
    const timer = setTimeout(() => {
      setSearch(filters.search.trim());
      setCursors([""]);
      setOpened(undefined);
    }, 350);
    return () => clearTimeout(timer);
  }, [filters.search, search]);
  function changeFilters(next: HistoryFilters) {
    setFilters(next);
    setCursors([""]);
    setOpened(undefined);
  }
  return (
    <section
      className="orders-section history-section"
      aria-labelledby="orders-history-heading"
      aria-busy={page.isFetching}
    >
      <div className="history-heading">
        <div>
          <h2 id="orders-history-heading">История действий</h2>
          <p>Что происходило с заказами и требуется ли ваше внимание.</p>
        </div>
        <button
          type="button"
          className="secondary"
          disabled={page.isFetching}
          onClick={() => void page.refetch()}
          title="Прочитать сохранённые статусы в STYK"
        >
          <Icon name="refresh" size="button" />
          Обновить статусы
        </button>
      </div>
      <OrdersHistoryToolbar filters={filters} onChange={changeFilters} />
      <HistoryFeedback
        loading={page.isLoading}
        fetching={page.isFetching}
        error={page.isError ? page.error : undefined}
        empty={!rows.length}
        filtered={!!(search || filters.status || filters.attention)}
        reset={() => {
          changeFilters(emptyFilters);
          setSearch("");
        }}
        retry={() => void page.refetch()}
      />
      {!page.isError && !!rows.length && (
        <OrdersHistoryTable rows={rows} onOpen={setOpened} />
      )}
      <HistoryPagination
        index={cursors.length}
        busy={page.isFetching}
        next={page.data?.next_cursor}
        onPrevious={() => {
          setOpened(undefined);
          setCursors((value) => value.slice(0, -1));
        }}
        onNext={() => {
          setOpened(undefined);
          setCursors((value) => [...value, page.data!.next_cursor!]);
        }}
      />
      {selected && (
        <OrderHistoryDetailDrawer
          key={selected.id}
          row={selected}
          scope={props.scope}
          now={now}
          fetching={page.isFetching}
          error={page.isError ? page.error : undefined}
          onRefresh={() => void page.refetch()}
          onClose={() => setOpened(undefined)}
          onRecoveryAuthorization={props.onRecoveryAuthorization}
        />
      )}
    </section>
  );
}
function useLatestOutcomes(
  data: ReturnType<typeof useOrdersHistory>["latest"]["data"],
  onLinks: Props["onLinks"],
  onOutcomes: Props["onOutcomes"],
) {
  useEffect(() => {
    const links: Record<string, string> = {};
    const states: Record<string, string> = {};
    for (const operation of data?.operations ?? [])
      for (const work of operation.targets ?? []) {
        const id =
          work.command.correlation?.customer_order_id ||
          (work.command.action.startsWith("mirror_")
            ? work.receipt?.external_id
            : undefined);
        if (id) links[work.command.posting_number] ??= id;
        states[work.command.posting_number] ??= work.state;
      }
    onLinks?.(links);
    onOutcomes?.(states);
  }, [data, onLinks, onOutcomes]);
}
function HistoryFeedback({
  loading,
  fetching,
  error,
  empty,
  filtered,
  reset,
  retry,
}: {
  loading: boolean;
  fetching: boolean;
  error: unknown;
  empty: boolean;
  filtered: boolean;
  reset: () => void;
  retry: () => void;
}) {
  if (loading)
    return (
      <p className="history-feedback" role="status">
        Читаем историю…
      </p>
    );
  if (error)
    return (
      <div className="history-feedback" role="alert">
        <p>{orderRequestError(error)}</p>
        <button
          type="button"
          className="secondary"
          disabled={fetching}
          onClick={retry}
        >
          Повторить загрузку истории
        </button>
      </div>
    );
  if (!empty)
    return (
      <span className="sr-only" role="status">
        {fetching ? "Обновляем историю…" : "История загружена"}
      </span>
    );
  return (
    <div className="history-feedback history-empty">
      <Icon name="history" />
      <h3>{filtered ? "Операции не найдены" : "Действий пока нет"}</h3>
      <p>
        {filtered
          ? "Введите номер отправления целиком или измените выбранные результаты."
          : "Здесь появятся действия с заказами и их результаты."}
      </p>
      {filtered && (
        <button type="button" className="secondary" onClick={reset}>
          Сбросить фильтры истории
        </button>
      )}
    </div>
  );
}
function HistoryPagination({
  index,
  busy,
  next,
  onPrevious,
  onNext,
}: {
  index: number;
  busy: boolean;
  next?: string;
  onPrevious: () => void;
  onNext: () => void;
}) {
  return (
    <footer className="history-footer">
      <p>
        {index === 1
          ? "Показаны последние операции"
          : "Показаны более ранние операции"}
      </p>
      <nav aria-label="Страницы истории">
        <button
          type="button"
          className="secondary"
          disabled={busy || index === 1}
          onClick={onPrevious}
        >
          <Icon name="chevron-left" size="button" />
          <span className="sr-only">Предыдущие операции</span>
        </button>
        <span aria-live="polite">Страница {index}</span>
        <button
          type="button"
          className="secondary"
          disabled={busy || !next}
          onClick={onNext}
        >
          Следующие
          <Icon name="chevron-right" size="button" />
        </button>
      </nav>
    </footer>
  );
}
