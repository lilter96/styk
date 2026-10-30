import { useEffect, useRef, useState } from "react";
import type { Product } from "./api";
import { Icon } from "./UI";

export const eligibleForQueue = (p: Product) =>
  p.archived === false &&
  p.target_id === null &&
  p.candidates.length > 0 &&
  !p.candidates_truncated;

function Thumbnail() {
  return (
    <span className="matching-page-thumb" aria-label="Нет изображения">
      <Icon name="products" />
    </span>
  );
}
function CopyArticle({ value }: { value?: string }) {
  const [message, setMessage] = useState("");
  return (
    <div className="matching-page-article">
      <span title={value}>{value || "—"}</span>
      {value && (
        <button
          className="matching-page-copy"
          aria-label={`Копировать артикул ${value}`}
          onClick={async () => {
            try {
              await navigator.clipboard.writeText(value);
              setMessage("Скопировано");
            } catch {
              setMessage("Не удалось скопировать");
            }
          }}
        >
          <svg
            width="15"
            height="15"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.6"
            aria-hidden="true"
          >
            <rect x="8" y="8" width="12" height="12" rx="2" />
            <path d="M16 8V5a2 2 0 0 0-2-2H5a2 2 0 0 0-2 2v9a2 2 0 0 0 2 2h3" />
          </svg>
        </button>
      )}
      <span role="status">{message}</span>
    </div>
  );
}

function MappingRow({
  p,
  selected,
  disabled,
  selectionDisabled,
  toggle,
  open,
}: {
  p: Product;
  selected: boolean;
  disabled: boolean;
  selectionDisabled: boolean;
  toggle: (checked: boolean) => void;
  open: () => void;
}) {
  const confirmed = p.match_status === "CONFIRMED";
  const conflict = p.match_status === "CONFLICT";
  const title = p.name || p.offer_id || p.product_id;
  return (
    <tr className={selected ? "is-selected" : ""}>
      <td data-label="Выбор">
        <input
          type="checkbox"
          aria-label={`Выбрать ${p.offer_id || p.product_id} для проверки`}
          checked={selected}
          disabled={disabled || selectionDisabled || !eligibleForQueue(p)}
          onChange={(e) => toggle(e.target.checked)}
        />
      </td>
      <td data-label="Товар в МойСклад">
        <div className="matching-page-product">
          <Thumbnail />
          <div className="matching-page-product-copy">
            <button
              className="mapping-select"
              aria-label={`Сопоставить ${p.offer_id || p.product_id}`}
              disabled={disabled}
              onClick={open}
            >
              <span title={p.target_name || p.target_id || undefined}>
                {p.target_name ||
                  (p.target_id ? "Товар МойСклад" : "Выберите товар МойСклад")}
              </span>
              <span aria-hidden="true">⌄</span>
            </button>
            {p.target_id ? (
              <small title={p.target_id}>ID: {p.target_id}</small>
            ) : (
              <small>
                {p.candidate_count
                  ? `Предложений: ${p.candidate_count}`
                  : "Соответствие не выбрано"}
              </small>
            )}
          </div>
        </div>
      </td>
      <td data-label="Артикул">
        <CopyArticle value={p.target_article} />
      </td>
      <td data-label="Товар в Ozon">
        <div className="matching-page-product">
          <Thumbnail />
          <div className="matching-page-product-copy">
            <strong title={title}>{title}</strong>
            <small title={p.offer_id}>Offer ID: {p.offer_id}</small>
            <small title={p.product_id}>ID: {p.product_id}</small>
            {p.sku && <small title={p.sku}>SKU: {p.sku}</small>}
          </div>
        </div>
      </td>
      <td data-label="Статус">
        <div className="matching-page-badges">
          <span
            className={`matching-page-badge ${confirmed ? "confirmed" : conflict ? "conflict" : "unmatched"}`}
            title={p.match_status}
          >
            {confirmed
              ? "Подтверждено"
              : conflict
                ? "Конфликт"
                : "Не сопоставлен"}
          </span>
          {p.archived === true && (
            <span className="matching-page-badge archived">Архивный</span>
          )}
          {p.archived === null && <small>Активность неизвестна</small>}
        </div>
      </td>
      <td data-label="Действия">
        <button
          className={`matching-page-action ${confirmed || conflict ? "secondary" : "primary"}`}
          aria-label={`${confirmed ? "Изменить соответствие" : conflict ? "Разобраться с соответствием" : "Открыть сопоставление"} ${p.offer_id || p.product_id}`}
          disabled={disabled}
          onClick={open}
        >
          {confirmed ? "Изменить" : conflict ? "Открыть" : "Сопоставить"}
        </button>
      </td>
    </tr>
  );
}

export function ProductMappingTable({
  items,
  selected,
  pending,
  toggle,
  selectPage,
  open,
  startQueue,
  canStartQueue,
  children,
}: {
  items: Product[];
  selected: Product[];
  pending: boolean;
  toggle: (p: Product, checked: boolean) => void;
  selectPage: (checked: boolean) => void;
  open: (p: Product) => void;
  startQueue: () => void;
  canStartQueue: boolean;
  children: React.ReactNode;
}) {
  const checkbox = useRef<HTMLInputElement>(null);
  const eligible = items.filter(eligibleForQueue);
  const selectedIds = new Set(selected.map((p) => p.product_id));
  const selectedOnPage = eligible.filter((p) =>
    selectedIds.has(p.product_id),
  ).length;
  useEffect(() => {
    if (checkbox.current)
      checkbox.current.indeterminate =
        selectedOnPage > 0 && selectedOnPage < eligible.length;
  }, [selectedOnPage, eligible.length]);
  return (
    <section
      className="mapping-table-card"
      aria-label="Товары для сопоставления"
    >
      <div className="matching-page-bulk">
        <label>
          <input
            ref={checkbox}
            type="checkbox"
            aria-label="Выбрать все на странице"
            checked={eligible.length > 0 && selectedOnPage === eligible.length}
            disabled={pending || !eligible.length}
            onChange={(e) => selectPage(e.target.checked)}
          />
          Выбрать все на странице
        </label>
        <span>Очередь до 100 товаров</span>
        <div>
          <span>Выбрано: {selected.length}</span>
          <button
            disabled={pending || !selected.length || !canStartQueue}
            onClick={startQueue}
          >
            Сопоставить выбранные
          </button>
        </div>
      </div>
      <div
        className="mapping-table-scroll"
        tabIndex={0}
        role="region"
        aria-label="Список товаров"
      >
        <table className="mapping-table">
          <thead>
            <tr>
              <th aria-label="Выбор" />
              <th>Товар в МойСклад</th>
              <th>Артикул</th>
              <th>Товар в Ozon</th>
              <th>Статус</th>
              <th>Действия</th>
            </tr>
          </thead>
          <tbody>
            {items.map((p) => (
              <MappingRow
                key={p.product_id}
                p={p}
                selected={selectedIds.has(p.product_id)}
                disabled={pending}
                selectionDisabled={
                  selected.length >= 100 &&
                  !selectedIds.has(p.product_id) &&
                  eligibleForQueue(p)
                }
                toggle={(checked) => toggle(p, checked)}
                open={() => open(p)}
              />
            ))}
          </tbody>
        </table>
      </div>
      {children}
    </section>
  );
}

export function MappingPagination({
  offset,
  size,
  total,
  length,
  hasNext,
  busy,
  setOffset,
  setSize,
}: {
  offset: number;
  size: number;
  total?: number;
  length: number;
  hasNext: boolean;
  busy: boolean;
  setOffset: (v: number) => void;
  setSize: (v: number) => void;
}) {
  const current = Math.floor(offset / size) + 1;
  const count =
    total === undefined ? current + (hasNext ? 1 : 0) : Math.ceil(total / size);
  const pages = [...new Set([1, current - 1, current, current + 1, count])]
    .filter((n) => n > 0 && n <= count)
    .sort((a, b) => a - b);
  return (
    <footer className="mapping-table-footer">
      <label>
        Показывать по:
        <select
          aria-label="Товаров на странице"
          value={size}
          disabled={busy}
          onChange={(e) => setSize(Number(e.target.value))}
        >
          {[20, 50, 100].map((n) => (
            <option value={n} key={n}>
              {n}
            </option>
          ))}
        </select>
      </label>
      <nav aria-label="Страницы товаров">
        <button
          className="matching-page-number"
          aria-label="Предыдущая страница"
          disabled={offset === 0 || busy}
          onClick={() => setOffset(Math.max(0, offset - size))}
        >
          ‹
        </button>
        {pages.map((n, i) => (
          <span key={n}>
            {i > 0 && n > pages[i - 1] + 1 && (
              <span className="matching-page-ellipsis">…</span>
            )}
            <button
              className="matching-page-number"
              aria-label={`Страница ${n}`}
              aria-current={n === current ? "page" : undefined}
              disabled={busy}
              onClick={() => setOffset((n - 1) * size)}
            >
              {n}
            </button>
          </span>
        ))}
        <button
          className="matching-page-number"
          aria-label="Следующая страница"
          disabled={!hasNext || busy}
          onClick={() => setOffset(offset + size)}
        >
          ›
        </button>
      </nav>
      <span>
        {length ? offset + 1 : 0}–{offset + length}
        {total === undefined ? "" : ` из ${total}`}
      </span>
    </footer>
  );
}
