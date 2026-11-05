import { useEffect, useRef, useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "./api";
import { Icon } from "./UI";
import {
  OrderDetailDrawer,
  type OrderDetailActions,
} from "./OrderDetailDrawer";
import {
  validateTargetStates,
  type CurrentPostingPage,
  type OrderTargetState,
} from "./OrdersTargetState";
import {
  orderQuantity,
  postingTotal,
  PostingStatus,
  PostingTargetStatus,
} from "./OrdersPresentation";
import {
  knownOrderField,
  orderDate,
  orderFieldText,
  orderRequestError,
  ordersURL,
  postingStatusLabels,
  sameOrderScope,
  type OrderScope,
  type OrderPosting,
} from "./OrdersTypes";
export { orderQuantity } from "./OrdersPresentation";

type Props = OrderDetailActions & {
  scope: OrderScope;
  selected: string[];
  disabled: boolean;
  uncertain: string[];
  links: Record<string, string>;
  onSelected: (numbers: string[]) => void;
  onTargetStates?: (states: OrderTargetState[] | undefined) => void;
  freshnessSeconds?: number;
  actionReset?: number;
  onReview?: () => void;
  reviewDisabled?: boolean;
  reviewing?: boolean;
  preview?: ReactNode;
  notice?: ReactNode;
  onDismissPreview?: () => void;
  onSourceChange?: () => void;
  onDetailChange?: (open: boolean) => void;
};

export function OrdersPostings(props: Props) {
  const {
    scope,
    selected,
    disabled,
    uncertain,
    links,
    onSelected,
    onTargetStates,
  } = props;
  const [search, setSearch] = useState("");
  const [query, setQuery] = useState("");
  const [status, setStatus] = useState("");
  const [attention, setAttention] = useState(false);
  const [cursors, setCursors] = useState<string[]>([""]);
  const [opened, setOpened] = useState<OrderPosting | null>(null);
  const [lookup, setLookup] = useState("");
  useEffect(() => {
    props.onDetailChange?.(!!opened);
  }, [opened, props.onDetailChange]);
  const all = useRef<HTMLInputElement>(null);
  const revisions = useRef(new Map<string, string>());
  useEffect(() => {
    if (search.trim() === query) return;
    const timer = setTimeout(() => {
      setQuery(search.trim());
      setCursors([""]);
    }, 350);
    return () => clearTimeout(timer);
  }, [search, query]);
  const cursor = cursors[cursors.length - 1];
  const page = useQuery({
    queryKey: ["orders-postings", scope, query, cursor, status, attention],
    queryFn: async ({ signal }) => {
      const value = await api<CurrentPostingPage>(
        ordersURL("postings", scope, {
          search: query,
          cursor,
          limit: "25",
          status,
          attention: String(attention),
        }),
        signal,
      );
      if (
        (value.postings ?? []).some(
          (item) => !sameOrderScope(item.scope, scope),
        )
      )
        throw new Error("posting scope mismatch");
      validateTargetStates(value, scope);
      return value;
    },
    retry: false,
    refetchOnWindowFocus: false,
    refetchInterval: 10000,
  });
  useEffect(() => {
    onTargetStates?.(page.data?.target_states);
  }, [page.data, onTargetStates]);
  useEffect(() => {
    let changed = false;
    for (const posting of page.data?.postings ?? []) {
      const previous = revisions.current.get(posting.posting_number);
      if (previous && previous !== posting.evidence.revision) changed = true;
      revisions.current.set(posting.posting_number, posting.evidence.revision);
    }
    if (changed) props.onSourceChange?.();
  }, [page.data, props.onSourceChange]);
  useEffect(() => {
    if (!lookup || query !== lookup || !page.data) return;
    const found = page.data.postings.find(
      (posting) => posting.posting_number === lookup,
    );
    if (found) {
      setOpened(found);
      setLookup("");
    }
  }, [lookup, query, page.data]);
  const postings = page.data?.postings ?? [];
  const states = new Map(
    page.data?.target_states?.map((state) => [state.posting_number, state]),
  );
  const locked = (posting: OrderPosting) =>
    states.get(posting.posting_number)?.unresolved ??
    uncertain.includes(posting.posting_number);
  const eligible = postings
    .filter((posting) => !locked(posting))
    .map((posting) => posting.posting_number);
  const selectedOnPage = eligible.filter((number) => selected.includes(number));
  useEffect(() => {
    if (all.current)
      all.current.indeterminate =
        selectedOnPage.length > 0 && selectedOnPage.length < eligible.length;
  }, [selectedOnPage.length, eligible.length]);
  const current = opened
    ? (postings.find(
        (posting) => posting.posting_number === opened.posting_number,
      ) ?? opened)
    : null;
  function open(posting: OrderPosting) {
    props.onDismissPreview?.();
    setOpened(posting);
  }
  function close() {
    props.onDismissPreview?.();
    setOpened(null);
  }
  function find(number: string) {
    props.onDismissPreview?.();
    const found = postings.find((posting) => posting.posting_number === number);
    if (found) {
      setOpened(found);
      return;
    }
    setOpened(null);
    setLookup(number);
    setStatus("");
    setAttention(false);
    setSearch(number);
    setQuery(number);
    setCursors([""]);
  }
  return (
    <section
      className="orders-section orders-table-section"
      aria-labelledby="orders-postings-heading"
    >
      <div className="orders-table-heading">
        <h2 id="orders-postings-heading">Отправления Ozon</h2>
        <button
          type="button"
          className={`orders-attention ${attention ? "active" : ""}`}
          aria-pressed={attention}
          disabled={disabled}
          onClick={() => {
            setAttention(!attention);
            setCursors([""]);
          }}
        >
          <Icon name="error" size="button" />
          Требуют внимания
        </button>
      </div>
      <div className="orders-toolbar">
        <label className="orders-search">
          <span className="sr-only">Найти отправление или заказ</span>
          <Icon name="search" size="button" />
          <input
            type="search"
            value={search}
            disabled={disabled}
            maxLength={256}
            placeholder="Номер отправления или заказа"
            onChange={(event) => setSearch(event.target.value)}
          />
        </label>
        <label>
          <span className="sr-only">Статус Ozon</span>
          <select
            value={status}
            disabled={disabled}
            onChange={(event) => {
              setStatus(event.target.value);
              setCursors([""]);
            }}
          >
            <option value="">Все статусы</option>
            {Object.entries(postingStatusLabels).map(([value, label]) => (
              <option key={value} value={value}>
                {label}
              </option>
            ))}
          </select>
        </label>
        <label>
          <span className="sr-only">Состояние отправлений</span>
          <select
            value={attention ? "attention" : ""}
            disabled={disabled}
            onChange={(event) => {
              setAttention(event.target.value === "attention");
              setCursors([""]);
            }}
          >
            <option value="">Все отправления</option>
            <option value="attention">Требуют внимания</option>
          </select>
        </label>
        <button
          type="button"
          className="secondary orders-refresh"
          aria-label="Обновить список отправлений"
          disabled={page.isFetching}
          onClick={() => void page.refetch()}
        >
          <Icon name="refresh" size="button" />
          Обновить
        </button>
      </div>
      <p className="orders-table-caption">
        Сохранённые данные Ozon. Обновление списка не запускает проверку
        источника.
      </p>
      <div className="orders-table-feedback" aria-live="polite">
        {page.isFetching && (
          <p role="status">
            {page.isLoading ? "Загружаем отправления…" : "Обновляем список…"}
          </p>
        )}
        {page.isError && (
          <p role="alert" className="orders-error">
            {orderRequestError(page.error)}{" "}
            <button
              type="button"
              className="text-action"
              onClick={() => void page.refetch()}
            >
              Повторить загрузку
            </button>
          </p>
        )}
        {!page.isLoading && !page.isError && !postings.length && (
          <div className="orders-empty">
            <Icon name="search" />
            <strong>
              {query || status || attention
                ? "Отправления не найдены"
                : "Отправлений пока нет"}
            </strong>
            <p>
              {query || status || attention
                ? "Измените поиск или сбросьте фильтры."
                : "Сохраните и включите настройки, затем запросите проверку источника."}
            </p>
            {(query || status || attention) && (
              <button
                type="button"
                className="text-action"
                onClick={() => {
                  setSearch("");
                  setQuery("");
                  setStatus("");
                  setAttention(false);
                  setLookup("");
                  setCursors([""]);
                }}
              >
                Сбросить фильтры
              </button>
            )}
          </div>
        )}
        {lookup &&
          !page.isFetching &&
          page.data &&
          !postings.some((posting) => posting.posting_number === lookup) && (
            <p className="orders-warning">
              Связанное отправление ещё не найдено в сохранённых данных.
              Запросите проверку источника.
            </p>
          )}
      </div>
      <div
        className="orders-table-scroll"
        role="region"
        aria-label="Таблица отправлений, доступна горизонтальная прокрутка"
        tabIndex={0}
      >
        <table
          className="orders-table"
          aria-label="Отправления Ozon"
          aria-busy={page.isFetching}
        >
          <thead>
            <tr>
              <th scope="col">
                <input
                  ref={all}
                  type="checkbox"
                  aria-label="Выбрать все доступные отправления на странице"
                  checked={
                    eligible.length > 0 &&
                    selectedOnPage.length === eligible.length
                  }
                  disabled={disabled || page.isFetching || !eligible.length}
                  onChange={(event) =>
                    onSelected(
                      event.target.checked
                        ? [...new Set([...selected, ...eligible])]
                        : selected.filter(
                            (number) => !eligible.includes(number),
                          ),
                    )
                  }
                />
              </th>
              <th scope="col">Отправление / заказ</th>
              <th scope="col">Статус Ozon</th>
              <th scope="col">Товары</th>
              <th scope="col">Схема / склад</th>
              <th scope="col">МойСклад</th>
              <th scope="col" className="orders-money">
                Сумма
              </th>
              <th scope="col">Проверено</th>
              <th scope="col">
                <span className="sr-only">Открыть</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {postings.map((posting) => (
              <tr
                key={posting.posting_number}
                className={
                  selected.includes(posting.posting_number) ? "is-selected" : ""
                }
                onClick={(event) => {
                  if (
                    !(event.target as HTMLElement).closest(
                      "button, input, a, label",
                    )
                  )
                    open(posting);
                }}
              >
                <td>
                  <input
                    type="checkbox"
                    aria-label={`Выбрать отправление ${posting.posting_number}`}
                    checked={selected.includes(posting.posting_number)}
                    disabled={disabled || locked(posting) || page.isError}
                    onChange={(event) =>
                      onSelected(
                        event.target.checked
                          ? [...new Set([...selected, posting.posting_number])]
                          : selected.filter(
                              (number) => number !== posting.posting_number,
                            ),
                      )
                    }
                  />
                </td>
                <td>
                  <button
                    type="button"
                    className="orders-posting-link"
                    title={posting.posting_number}
                    aria-label={`Открыть отправление ${posting.posting_number}`}
                    onClick={() => open(posting)}
                  >
                    {posting.posting_number}
                  </button>
                  <small title={knownOrderField(posting.order_number)}>
                    Заказ {orderFieldText(posting.order_number)}
                  </small>
                </td>
                <td>
                  <PostingStatus posting={posting} />
                </td>
                <td className="orders-quantity">
                  {posting.lines?.length ?? 0} поз. ·{" "}
                  {orderQuantity(posting.lines ?? [])} шт.
                </td>
                <td>
                  <span>{orderFieldText(posting.scheme)}</span>
                  <small title={knownOrderField(posting.warehouse_id)}>
                    {orderFieldText(posting.warehouse_id)}
                  </small>
                </td>
                <td>
                  <PostingTargetStatus
                    local={states.get(posting.posting_number)}
                    locked={locked(posting)}
                  />
                </td>
                <td className="orders-money">{postingTotal(posting)}</td>
                <td className="orders-checked">
                  <time
                    dateTime={posting.evidence.checked_at}
                    title={`Получено: ${orderDate(posting.evidence.observed_at)}`}
                  >
                    {orderDate(posting.evidence.checked_at)}
                  </time>
                </td>
                <td>
                  <button
                    type="button"
                    className="orders-row-open"
                    aria-label={`Подробнее об отправлении ${posting.posting_number}`}
                    onClick={() => open(posting)}
                  >
                    <Icon name="chevron-right" size="button" />
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <footer className="orders-table-footer">
        <div className="orders-bulk-actions">
          <span>Выбрано отправлений: {selected.length}</span>
          {props.onReview && (
            <button
              type="button"
              className="secondary"
              disabled={props.reviewDisabled || !selected.length}
              onClick={props.onReview}
            >
              {props.reviewing
                ? "Проверяем изменения…"
                : "Проверить выбранные отправления"}
            </button>
          )}
          {selected.length > 0 && (
            <button
              type="button"
              className="text-action"
              disabled={disabled}
              onClick={() => onSelected([])}
            >
              Снять выбор
            </button>
          )}
        </div>
        <div className="orders-pagination">
          <span>
            Страница {cursors.length}
            {postings.length > 0 ? ` · ${postings.length} отправлений` : ""}
          </span>
          <button
            type="button"
            className="secondary"
            aria-label="Предыдущие отправления"
            disabled={cursors.length === 1 || page.isFetching}
            onClick={() => setCursors((value) => value.slice(0, -1))}
          >
            <Icon name="chevron-left" size="button" />
          </button>
          <button
            type="button"
            className="secondary"
            aria-label="Следующие отправления"
            disabled={!page.data?.next_cursor || page.isFetching}
            onClick={() =>
              setCursors((value) => [...value, page.data!.next_cursor!])
            }
          >
            <Icon name="chevron-right" size="button" />
          </button>
        </div>
      </footer>
      {current && (
        <OrderDetailDrawer
          {...props}
          key={current.posting_number}
          posting={current}
          local={states.get(current.posting_number)}
          link={links[current.posting_number]}
          disabled={disabled || page.isError}
          locked={locked(current)}
          freshnessSeconds={props.freshnessSeconds ?? 300}
          actionReset={props.actionReset ?? 0}
          onClose={close}
          onFind={find}
        />
      )}
    </section>
  );
}
