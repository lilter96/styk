import { useRef, useState } from "react";
import type { UseQueryResult } from "@tanstack/react-query";
import type { Page, Product } from "./api";
import type { MappingTarget } from "./ProductReview";
import { Icon, ProviderBadge } from "./UI";

function CopyValue({ label, value }: { label: string; value: string }) {
  const [message, setMessage] = useState("");
  return (
    <div className="matching-identifier">
      <dt>{label}</dt>
      <dd>
        {value}
        <button
          aria-label={`Копировать ${label}`}
          onClick={async () => {
            try {
              await navigator.clipboard.writeText(value);
              setMessage("Скопировано");
            } catch {
              setMessage("Не удалось скопировать");
            }
          }}
        >
          ⧉
        </button>
        <span role="status">{message}</span>
      </dd>
    </div>
  );
}

export function OzonProductPanel({ product }: { product: Product }) {
  return (
    <aside className="matching-ozon">
      <header>
        <ProviderBadge provider="ozon" />
        <b>Товар в Ozon</b>
        <span
          className={`mapping-chip ${product.match_status === "CONFIRMED" ? "ok" : "warning"}`}
        >
          {product.match_status === "CONFIRMED"
            ? "Подтверждён"
            : product.match_status === "CONFLICT"
              ? "Конфликт"
              : "Не сопоставлен"}
        </span>
      </header>
      <div className="matching-source-title">
        <div className="matching-image-placeholder">
          <Icon name="products" />
        </div>
        <h3>{product.name || product.offer_id || product.product_id}</h3>
      </div>
      <dl>
        <CopyValue label="Offer ID" value={product.offer_id} />
        <CopyValue label="ID товара Ozon" value={product.product_id} />
        {product.sku && <CopyValue label="SKU" value={product.sku} />}
      </dl>
      {product.archived !== null && (
        <p className="matching-muted">
          {product.archived ? "Архивный товар" : "Активный товар"}
        </p>
      )}
      {product.target_id && (
        <div className="matching-current">
          <h4>Текущая связь</h4>
          <b>{product.target_name || product.target_id}</b>
          {product.target_article && <p>Артикул: {product.target_article}</p>}
          <p>Изменение проходит серверную проверку конфликтов.</p>
        </div>
      )}
    </aside>
  );
}

type TargetPage = Page<MappingTarget> & {
  total?: number;
  page?: number;
  pageSize?: number;
};
type SearchProps = {
  search: string;
  setSearch: (value: string) => void;
  searchBy: string;
  setSearchBy: (value: string) => void;
  inStockOnly: boolean;
  setInStockOnly: (value: boolean) => void;
  pageSize: number;
  setPageSize: (value: number) => void;
  offset: number;
  setOffset: (value: number) => void;
  query: UseQueryResult<TargetPage>;
  selected: MappingTarget | null;
  select: (value: MappingTarget) => void;
  disabled: boolean;
};

export function TargetSearchPanel(p: SearchProps) {
  const input = useRef<HTMLInputElement>(null);
  const rows = useRef<HTMLDivElement>(null);
  const total = p.query.data?.total;
  const page = p.offset / p.pageSize + 1;
  const pageCount =
    total === undefined ? undefined : Math.ceil(total / p.pageSize);
  const pages = pageCount
    ? [...new Set([1, page - 1, page, page + 1, pageCount])]
        .filter((n) => n >= 1 && n <= pageCount)
        .sort((a, b) => a - b)
    : [];
  return (
    <section className="matching-search-panel">
      <h3>
        <ProviderBadge provider="moysklad" /> Поиск товара в МойСклад
      </h3>
      <div className="matching-search-line">
        <label>
          <Icon name="diagnostics" />
          <input
            ref={input}
            aria-label="Поиск товара МойСклад"
            placeholder="Поиск по SKU, артикулу, названию или ID"
            value={p.search}
            disabled={p.disabled}
            onChange={(e) => p.setSearch(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") e.preventDefault();
              if (e.key === "ArrowDown") {
                e.preventDefault();
                rows.current
                  ?.querySelector<HTMLButtonElement>("button:not(:disabled)")
                  ?.focus();
              }
            }}
          />
          {p.search && (
            <button
              aria-label="Очистить поиск"
              onClick={() => {
                p.setSearch("");
                input.current?.focus();
              }}
            >
              ×
            </button>
          )}
        </label>
        <span aria-live="polite">
          {p.query.isFetching
            ? "Поиск…"
            : total === undefined
              ? ""
              : `Найдено ${total} товаров`}
        </span>
      </div>
      <div className="matching-search-filters">
        <div>
          <span>Искать по:</span>
          {[
            ["all", "Все поля"],
            ["sku", "SKU"],
            ["code", "Код"],
            ["article", "Артикул"],
            ["name", "Название"],
            ["id", "ID"],
          ].map(([key, label]) => (
            <button
              key={key}
              disabled={p.disabled || key === "sku"}
              title={
                key === "sku"
                  ? "SKU не импортируется из МойСклад. Используйте код или артикул."
                  : undefined
              }
              aria-pressed={p.searchBy === key}
              onClick={() => p.setSearchBy(key)}
            >
              {label}
            </button>
          ))}
        </div>
        <div>
          <span>Показывать:</span>
          <select
            aria-label="Активность товаров"
            disabled
            title="Активность пока не импортируется"
          >
            <option>Все</option>
          </select>
          <select
            aria-label="Наличие товаров"
            disabled={p.disabled}
            value={String(p.inStockOnly)}
            onChange={(e) => p.setInStockOnly(e.target.value === "true")}
          >
            <option value="false">Все остатки</option>
            <option value="true">В наличии</option>
          </select>
        </div>
      </div>
      <div
        className="matching-results"
        ref={rows}
        aria-label="Товары МойСклад"
        aria-busy={p.query.isFetching}
        onKeyDown={(e) => {
          if (e.key !== "ArrowDown" && e.key !== "ArrowUp") return;
          const buttons = Array.from(
            rows.current?.querySelectorAll<HTMLButtonElement>(
              "button:not(:disabled)",
            ) || [],
          );
          const index = buttons.indexOf(
            document.activeElement as HTMLButtonElement,
          );
          e.preventDefault();
          buttons[
            (index + (e.key === "ArrowDown" ? 1 : -1) + buttons.length) %
              buttons.length
          ]?.focus();
        }}
      >
        {p.query.isLoading ? (
          <div role="status" aria-label="Загружаем товары">
            {Array.from({ length: 5 }, (_, i) => (
              <div className="matching-skeleton" key={i} />
            ))}
          </div>
        ) : p.query.error ? (
          <div role="alert" className="matching-empty">
            Поиск товаров недоступен.
            <button onClick={() => void p.query.refetch()}>Повторить</button>
          </div>
        ) : !p.query.data?.items.length ? (
          <p className="matching-empty">
            Товары не найдены. Попробуйте другой запрос.
          </p>
        ) : (
          p.query.data.items.map((t) => (
            <TargetRow
              key={t.kind + ":" + t.id}
              target={t}
              selected={p.selected?.kind === t.kind && p.selected.id === t.id}
              disabled={p.disabled || p.query.isFetching}
              select={() => p.select(t)}
            />
          ))
        )}
      </div>
      <div className="matching-pagination">
        <span>
          {total === undefined
            ? ""
            : total === 0
              ? "0 из 0"
              : `${p.offset + 1}–${Math.min(p.offset + (p.query.data?.items.length || 0), total)} из ${total}`}
        </span>
        <div>
          <button
            aria-label="Предыдущие товары"
            disabled={p.offset === 0 || p.query.isFetching}
            onClick={() => p.setOffset(Math.max(0, p.offset - p.pageSize))}
          >
            ‹
          </button>
          {pages.map((n, i) => (
            <span key={n}>
              {i > 0 && n > pages[i - 1] + 1 && <span>…</span>}
              <button
                aria-label={`Страница ${n}`}
                aria-current={page === n ? "page" : undefined}
                disabled={p.query.isFetching}
                onClick={() => p.setOffset((n - 1) * p.pageSize)}
              >
                {n}
              </button>
            </span>
          ))}
          <button
            aria-label="Следующие товары"
            disabled={!p.query.data?.has_next || p.query.isFetching}
            onClick={() => p.setOffset(p.offset + p.pageSize)}
          >
            ›
          </button>
        </div>
        <select
          aria-label="Товаров на странице"
          value={p.pageSize}
          onChange={(e) => p.setPageSize(Number(e.target.value))}
        >
          {[10, 20, 50, 100].map((n) => (
            <option key={n} value={n}>
              {n} на странице
            </option>
          ))}
        </select>
      </div>
    </section>
  );
}

function TargetRow({
  target: t,
  selected,
  disabled,
  select,
}: {
  target: MappingTarget;
  selected: boolean;
  disabled: boolean;
  select: () => void;
}) {
  return (
    <button
      className={`matching-result ${selected ? "selected" : ""}`}
      aria-label={`${t.name || "Без названия"}, ${t.kind === "variant" ? "вариант" : "товар"}`}
      aria-pressed={selected}
      disabled={disabled}
      onClick={select}
    >
      <span className="matching-radio" />
      <span className="matching-thumb">
        <Icon name="products" />
      </span>
      <span className="matching-row-copy">
        <b>{t.name || "Без названия"}</b>
        <small>
          Код: {t.code || "не указан"} · Артикул: {t.article || "не указан"}
        </small>
        <small>ID: {t.id}</small>
      </span>
      <span className="matching-row-status">
        {t.mapped
          ? "Уже сопоставлен"
          : t.kind === "variant"
            ? "Вариант товара"
            : "Товар"}
      </span>
      <span className="matching-quantity">
        {t.quantity ?? "—"}
        <small>{t.quantity == null ? "остаток неизвестен" : "доступно"}</small>
      </span>
    </button>
  );
}
