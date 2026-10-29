import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "./api";
import { OrdersActionEligibility } from "./OrdersActionEligibility";
import {
  knownOrderField,
  orderDate,
  orderFieldText,
  orderMoney,
  orderRequestError,
  ordersURL,
  postingStatusLabels,
  sameOrderScope,
  MoyOrderLink,
  type OrderScope,
  type OrderPosting,
  type OrdersPostingPage,
} from "./OrdersTypes";

export function orderQuantity(lines: OrderPosting["lines"]): string {
  if (
    !lines.length ||
    lines.some((line) => !/^\d+(\.\d+)?$/.test(line.quantity))
  )
    return "Неизвестно";
  const scale = Math.max(
    ...lines.map((line) => line.quantity.split(".")[1]?.length ?? 0),
  );
  const sum = lines.reduce(
    (total, line) =>
      total +
      BigInt(
        line.quantity
          .replace(".", "")
          .padEnd(
            line.quantity.replace(".", "").length +
              scale -
              (line.quantity.split(".")[1]?.length ?? 0),
            "0",
          ),
      ),
    0n,
  );
  if (!scale) return String(sum);
  const digits = String(sum).padStart(scale + 1, "0");
  return `${digits.slice(0, -scale)}.${digits.slice(-scale)}`.replace(
    /\.?0+$/,
    "",
  );
}
function postingTotal(posting: OrderPosting) {
  const money = knownOrderField(posting.financial?.order_total);
  if (money !== undefined) return orderMoney(money);
  const amount = knownOrderField(posting.financial?.order_total_amount);
  const currency = knownOrderField(posting.financial?.currency);
  if (amount !== undefined)
    return `${amount} ${currency ?? "(валюта неизвестна)"}`;
  return orderFieldText(posting.financial?.order_total);
}
export function OrdersPostings({
  scope,
  selected,
  disabled,
  uncertain,
  links,
  onSelected,
  freshnessSeconds = 300,
}: {
  scope: OrderScope;
  selected: string[];
  disabled: boolean;
  uncertain: string[];
  links: Record<string, string>;
  onSelected: (numbers: string[]) => void;
  freshnessSeconds?: number;
}) {
  const [search, setSearch] = useState("");
  const [query, setQuery] = useState("");
  const [cursors, setCursors] = useState<string[]>([""]);
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
    queryKey: ["orders-postings", scope, query, cursor],
    queryFn: async ({ signal }) => {
      const value = await api<OrdersPostingPage>(
        ordersURL("postings", scope, { search: query, cursor, limit: "25" }),
        signal,
      );
      if (
        (value.postings ?? []).some(
          (item) => !sameOrderScope(item.scope, scope),
        )
      )
        throw new Error("posting scope mismatch");
      return value;
    },
    retry: false,
    refetchOnWindowFocus: false,
  });
  const postings = page.data?.postings ?? [];
  function findPosting(number: string) {
    setSearch(number);
  }
  return (
    <section
      className="orders-section"
      aria-labelledby="orders-postings-heading"
    >
      <div className="orders-section-heading">
        <h2 id="orders-postings-heading">Отправления Ozon</h2>
        <button
          type="button"
          className="secondary"
          disabled={page.isFetching}
          onClick={() => void page.refetch()}
        >
          Обновить список отправлений
        </button>
      </div>
      <label className="orders-search">
        Найти отправление или заказ
        <input
          type="search"
          value={search}
          placeholder="Номер отправления или заказа"
          onChange={(event) => setSearch(event.target.value)}
        />
      </label>
      <p className="orders-note">
        Обновление списка читает сохранённые данные. «Проверить источник сейчас»
        запрашивает новые данные Ozon.
      </p>
      {page.isFetching && <p role="status">Загружаем отправления…</p>}
      {page.isError && (
        <p role="alert" className="orders-error">
          {orderRequestError(page.error)}
        </p>
      )}
      {!page.isLoading && !page.isError && !postings.length && (
        <p className="orders-empty">
          Отправлений пока нет{query ? " по этому запросу" : ""}.{" "}
          {query
            ? "Измените поиск."
            : "Сохраните и включите настройки, затем запросите проверку источника."}
        </p>
      )}
      <div className="orders-postings">
        {postings.map((posting) => {
          const status = knownOrderField(posting.status);
          const parent = knownOrderField(posting.parent_posting_number);
          const locked = uncertain.includes(posting.posting_number);
          return (
            <article className="orders-posting" key={posting.posting_number}>
              <div className="orders-posting-title">
                <label className="orders-check">
                  <input
                    type="checkbox"
                    aria-label={`Выбрать отправление ${posting.posting_number}`}
                    checked={selected.includes(posting.posting_number)}
                    disabled={disabled || locked}
                    onChange={(event) =>
                      onSelected(
                        event.target.checked
                          ? [...selected, posting.posting_number]
                          : selected.filter(
                              (number) => number !== posting.posting_number,
                            ),
                      )
                    }
                  />
                  <strong>{posting.posting_number}</strong>
                </label>
                <span
                  className={`orders-badge ${status === "cancelled" ? "warning" : ""}`}
                >
                  {status
                    ? (postingStatusLabels[status] ?? status)
                    : orderFieldText(posting.status)}
                </span>
              </div>
              <dl className="orders-facts">
                <div>
                  <dt>Заказ Ozon</dt>
                  <dd>
                    {orderFieldText(posting.order_number)}
                    <small>ID {posting.commercial_order_id}</small>
                  </dd>
                </div>
                <div>
                  <dt>Схема / склад Ozon</dt>
                  <dd>
                    {orderFieldText(posting.scheme)} /{" "}
                    {orderFieldText(posting.warehouse_id)}
                  </dd>
                </div>
                <div>
                  <dt>Количество</dt>
                  <dd>{orderQuantity(posting.lines ?? [])}</dd>
                </div>
                <div>
                  <dt>Сумма Ozon</dt>
                  <dd>{postingTotal(posting)}</dd>
                </div>
                <div>
                  <dt>Обновлено в источнике</dt>
                  <dd>
                    {orderFieldText(
                      posting.evidence.provider_updated_at,
                      orderDate,
                    )}
                  </dd>
                </div>
                <div>
                  <dt>Проверено STYK</dt>
                  <dd>
                    {orderDate(posting.evidence.checked_at)}
                    <small>
                      Получено {orderDate(posting.evidence.observed_at)}
                    </small>
                  </dd>
                </div>
              </dl>
              <p className="orders-note">
                <MoyOrderLink id={links[posting.posting_number]} />
              </p>
              {locked && (
                <p className="orders-warning">
                  Результат подтверждения неизвестен. Повторная отправка
                  запрещена до сверки.
                </p>
              )}
              <details>
                <summary>Условия для действий в Ozon</summary>
                <OrdersActionEligibility
                  posting={posting}
                  freshnessSeconds={freshnessSeconds}
                />
              </details>
              {(parent || posting.child_posting_numbers?.length > 0) && (
                <div className="orders-split">
                  {parent && (
                    <button
                      type="button"
                      className="text-action"
                      onClick={() => findPosting(parent)}
                    >
                      Родительское отправление {parent}
                    </button>
                  )}
                  {(posting.child_posting_numbers ?? []).map((number) => (
                    <button
                      className="text-action"
                      type="button"
                      key={number}
                      onClick={() => findPosting(number)}
                    >
                      Дочернее отправление {number}
                    </button>
                  ))}
                </div>
              )}
              <details>
                <summary>
                  Позиции и сведения источника ({posting.lines?.length ?? 0})
                </summary>
                <div className="orders-detail-body">
                  {(posting.lines ?? []).map((line, index) => (
                    <div
                      className="orders-line"
                      key={`${line.identity.provider_line_id ?? line.identity.sku ?? line.identity.offer_id}:${index}`}
                    >
                      <strong>{orderFieldText(line.name)}</strong>
                      <span>
                        Артикул {line.identity.offer_id ?? "неизвестен"} · SKU{" "}
                        {line.identity.sku ?? "неизвестен"}
                      </span>
                      <span>
                        Количество: {line.quantity || "Неизвестно"} · Цена:{" "}
                        {orderFieldText(line.price, orderMoney)}
                      </span>
                      <span>
                        Отменено: {orderFieldText(line.cancelled_quantity)}
                      </span>
                    </div>
                  ))}
                  {knownOrderField(posting.substatus) !== undefined && (
                    <p>Подстатус: {orderFieldText(posting.substatus)}</p>
                  )}
                  {(posting.drift ?? []).map((item, index) => (
                    <p className="orders-warning" key={index}>
                      Изменение схемы источника: <code>{item.path}</code> ·{" "}
                      <code>{item.code}</code>. Нужна проверка совместимости.
                    </p>
                  ))}
                </div>
              </details>
            </article>
          );
        })}
      </div>
      <div className="orders-actions">
        <button
          className="secondary"
          type="button"
          disabled={cursors.length === 1 || page.isFetching}
          onClick={() => setCursors((value) => value.slice(0, -1))}
        >
          Предыдущие отправления
        </button>
        <span>Страница {cursors.length}</span>
        <button
          className="secondary"
          type="button"
          disabled={!page.data?.next_cursor || page.isFetching}
          onClick={() =>
            setCursors((value) => [...value, page.data!.next_cursor!])
          }
        >
          Следующие отправления
        </button>
        <span>Выбрано отправлений: {selected.length}</span>
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
    </section>
  );
}
