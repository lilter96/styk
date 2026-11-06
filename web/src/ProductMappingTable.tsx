import { useEffect, useRef } from "react";
import type { CatalogConfig } from "./CatalogSyncTypes";
import { BrandLogo } from "./BrandLogo";
import type { Product } from "./api";
import { Icon } from "./UI";

export const eligibleForQueue = (p: Product) =>
  p.provider !== "moysklad" &&
  p.observed !== false &&
  p.archived === false &&
  (p.match_status === "CONFIRMED" ||
    (p.target_id === null && p.candidates.length > 0)) &&
  !p.candidates_truncated;

function Thumbnail() {
  return (
    <span className="matching-page-thumb" aria-label="Нет изображения">
      <Icon name="products" />
    </span>
  );
}
function MappingRow({
  p,
  selected,
  disabled,
  selectionDisabled,
  toggle,
  open,
  config,
}: {
  p: Product;
  config?: CatalogConfig;
  selected: boolean;
  disabled: boolean;
  selectionDisabled: boolean;
  toggle: (checked: boolean) => void;
  open: () => void;
}) {
  const confirmed = p.match_status === "CONFIRMED";
  const conflict = p.match_status === "CONFLICT";
  const title = p.name || p.offer_id || "Товар без названия";
  const enrollment = config?.targets.find((t) =>
    [t.source, t.target].some(
      (identity) =>
        identity.account.provider === "ozon" && identity.id === p.product_id,
    ),
  );
  const automatic =
    confirmed &&
    config?.enabled &&
    config.test_enabled &&
    enrollment?.automatic;
  return (
    <tr className={selected ? "is-selected" : ""}>
      <td data-label="Выбор">
        <label className="products-select-control">
          <input
            type="checkbox"
            aria-label={`Выбрать ${p.offer_id || p.product_id} для проверки`}
            checked={selected}
            disabled={disabled || selectionDisabled || !eligibleForQueue(p)}
            onChange={(e) => toggle(e.target.checked)}
          />
        </label>
      </td>
      <>
        <td data-label="Товар">
          <div className="matching-page-product">
            <Thumbnail />
            <div className="matching-page-product-copy">
              <strong>{title}</strong>
              <small>
                <BrandLogo provider={p.provider} decorative />{" "}
                {p.offer_id || "Артикул не указан"}
              </small>
            </div>
          </div>
        </td>
        <td data-label="В другом сервисе">
          <div className="products-paired">
            <BrandLogo
              provider={p.provider === "moysklad" ? "ozon" : "moysklad"}
              decorative
            />
            <div>
              {p.target_name ||
                (p.target_id
                  ? "Товар МойСклад"
                  : p.provider === "moysklad"
                    ? "Нет карточки Ozon"
                    : "Нет подтверждённой пары")}
              <small>{p.target_article}</small>
            </div>
          </div>
        </td>
      </>
      <td data-label="Статус">
        <div className="matching-page-badges">
          <span
            className={`matching-page-badge ${confirmed ? "confirmed" : conflict ? "conflict" : "unmatched"}`}
          >
            {confirmed ? "Связан" : conflict ? "Конфликт" : "Нет пары"}
          </span>
          {p.observed === false && <small>Нет в последней загрузке</small>}
          {p.shared_inventory && (
            <small>Общий остаток для нескольких предложений</small>
          )}
          {p.kind === "variant" && <small>Модификация товара</small>}
          {p.archived === true && (
            <span className="matching-page-badge archived">Архивный</span>
          )}
          {p.archived === null && <small>Активность неизвестна</small>}
        </div>
      </td>
      <td data-label="Передача данных">
        <span className={`products-transfer ${automatic ? "automatic" : ""}`}>
          {!confirmed
            ? "Недоступна"
            : !config
              ? "Нет данных о настройках"
              : automatic
                ? "Автоматически"
                : !config.enabled || !config.test_enabled
                  ? "Выключена"
                  : "При подтверждении"}
        </span>
      </td>
      <td data-label="Действия">
        <button
          className={`matching-page-action ${confirmed || conflict ? "secondary" : "primary"}`}
          aria-label={`${confirmed ? "Изменить соответствие" : conflict ? "Разобраться с соответствием" : "Открыть сопоставление"} ${p.offer_id || p.product_id}`}
          disabled={disabled}
          onClick={open}
        >
          {p.provider === "moysklad"
            ? "Открыть"
            : confirmed
              ? "Открыть"
              : conflict
                ? "Разобраться"
                : "Сопоставить"}
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
  config,
}: {
  items: Product[];
  config?: CatalogConfig;
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
      <div className="matching-page-bulk products-table-selection">
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
        <span>Проверка ничего не отправляет</span>
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
              <th>Товар</th>
              <th>В другом сервисе</th>
              <th>Связь</th>
              <th>Передача данных</th>
              <th>Действия</th>
            </tr>
          </thead>
          <tbody>
            {items.map((p) => (
              <MappingRow
                key={`${p.provider}:${p.kind}:${p.product_id}`}
                p={p}
                config={config}
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
          <Icon name="chevron-left" size="button" />
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
          <Icon name="chevron-right" size="button" />
        </button>
      </nav>
      <span>
        {length ? offset + 1 : 0}–{offset + length}
        {total === undefined ? "" : ` из ${total}`}
      </span>
    </footer>
  );
}
