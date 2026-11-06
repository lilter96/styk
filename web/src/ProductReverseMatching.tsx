import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { api, params, type Page, type Product } from "./api";
import type { Confirmation, Scope } from "./Confirmation";
import { IntegrationDialog } from "./IntegrationDialog";
import { BrandLogo } from "./BrandLogo";
import { ProductReview } from "./ProductReview";

export function ProductReverseMatching({
  product,
  scope,
  confirmation,
  close,
}: {
  product: Product;
  scope: Scope;
  confirmation: Confirmation;
  close: () => void;
}) {
  const [search, setSearch] = useState(product.offer_id || "");
  const [query, setQuery] = useState(search);
  const [offset, setOffset] = useState(0);
  const [selected, setSelected] = useState<Product | null>(null);
  const [reviewing, setReviewing] = useState(false);
  useEffect(() => {
    const timer = setTimeout(() => {
      setQuery(search);
      setOffset(0);
    }, 300);
    return () => clearTimeout(timer);
  }, [search]);
  const result = useQuery({
    queryKey: ["reverse-product-matching", scope, query, offset],
    queryFn: ({ signal }) =>
      api<Page<Product>>(
        "/api/products?" +
          params({
            tenant: scope.tenant,
            moysklad_account: scope.moy,
            ozon_account: scope.ozon,
            provider: "ozon",
            search: query,
            offset,
            pageSize: 20,
          }),
        signal,
      ),
    retry: false,
  });
  if (reviewing && selected)
    return (
      <ProductReview
        key={selected.product_id}
        product={selected}
        scope={scope}
        confirmation={confirmation}
        initialTarget={{
          id: product.product_id,
          kind: product.kind,
          name: product.name || undefined,
          article: product.offer_id || "",
          code: "",
        }}
        close={close}
      />
    );
  return (
    <IntegrationDialog
      title="Сопоставить товар"
      className="products-reverse-dialog"
      onClose={close}
      initialFocus="input[type=search]"
    >
      <section className="products-reverse-source">
        <BrandLogo provider="moysklad" decorative />
        <div>
          <h3>{product.name || product.offer_id || "Товар МойСклад"}</h3>
          <p>Артикул: {product.offer_id || "Не указан"}</p>
        </div>
        <span className="mapping-chip warning">Нет пары</span>
      </section>
      <p className="products-notice">
        Найдите этот же товар в Ozon. Совпадение названия или артикула само по
        себе не подтверждает соответствие.
      </p>
      <label className="products-reverse-search">
        Поиск товара Ozon
        <input
          type="search"
          value={search}
          placeholder="Название, артикул или SKU в Ozon"
          onChange={(e) => {
            setSearch(e.target.value);
            setSelected(null);
          }}
        />
      </label>
      <h3>Возможные соответствия</h3>
      {result.isFetching && <p role="status">Ищем товары Ozon…</p>}
      {result.isError && (
        <p role="alert">
          Не удалось загрузить товары.{" "}
          <button className="secondary" onClick={() => void result.refetch()}>
            Повторить
          </button>
        </p>
      )}
      <div className="products-reverse-candidates">
        {result.data?.items.map((item) => (
          <label
            key={item.product_id}
            className={
              selected?.product_id === item.product_id ? "selected" : ""
            }
          >
            <input
              type="radio"
              name="reverse-candidate"
              checked={selected?.product_id === item.product_id}
              onChange={() => setSelected(item)}
              disabled={item.archived === true || item.observed === false}
            />
            <BrandLogo provider="ozon" decorative />
            <div>
              <b>{item.name || item.offer_id}</b>
              <small>{item.offer_id}</small>
              {item.match_status === "CONFIRMED" && (
                <span className="mapping-chip warning">
                  Уже есть связь — требуется проверка
                </span>
              )}
            </div>
          </label>
        ))}
      </div>
      {!result.isFetching && !result.data?.items.length && (
        <p className="catalog-empty">
          Совпадений нет. Измените поиск или подготовьте новую карточку Ozon.
        </p>
      )}
      <div className="catalog-actions">
        <button
          className="secondary"
          disabled={!offset || result.isFetching}
          onClick={() => {
            setOffset(Math.max(0, offset - 20));
            setSelected(null);
          }}
        >
          Назад
        </button>
        <button
          className="secondary"
          disabled={!result.data?.has_next || result.isFetching}
          onClick={() => {
            setOffset(offset + 20);
            setSelected(null);
          }}
        >
          Далее
        </button>
      </div>
      <footer className="products-reverse-footer">
        <Link to="/products/import/moysklad-to-ozon">
          Не нашли? Создать товар в Ozon
        </Link>
        <button
          disabled={!selected || result.isFetching || result.isError}
          onClick={() => setReviewing(true)}
        >
          Проверить выбранную пару →
        </button>
      </footer>
    </IntegrationDialog>
  );
}
