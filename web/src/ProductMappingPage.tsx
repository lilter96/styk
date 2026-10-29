import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { api, params, type Page, type Product, type Report } from "./api";
import { useConfirmation, type Confirmation, type Scope } from "./Confirmation";
import { ProductReview, type MappingTarget } from "./ProductReview";

const statuses: Record<string, { label: string; tone: string }> = {
  CONFIRMED: { label: "Подтверждено", tone: "ok" },
  ONE_CANDIDATE: { label: "Проверьте и подтвердите", tone: "blue" },
  AMBIGUOUS: { label: "Нужно выбрать товар", tone: "warning" },
  NO_CANDIDATE: { label: "Не найдено", tone: "warning" },
  CONFLICT: { label: "Есть конфликт", tone: "warning" },
  INSUFFICIENT_DATA: { label: "Недостаточно данных", tone: "neutral" },
};

export function ProductMappingPage({
  scope,
  confirmation,
}: {
  scope: Scope;
  confirmation?: Confirmation;
}) {
  const localConfirmation = useConfirmation();
  const activeConfirmation = confirmation ?? localConfirmation;
  const [review, setReview] = useState<Product | null>(null);
  const [search, setSearch] = useState("");
  const [archived, setArchived] = useState("");
  const [mapping, setMapping] = useState("");
  const [offset, setOffset] = useState(0);
  const [selectedProducts, setSelectedProducts] = useState<Product[]>([]);
  const ready = !!(scope.tenant && scope.moy && scope.ozon);
  const base = {
    tenant: scope.tenant,
    moysklad_account: scope.moy,
    ozon_account: scope.ozon,
  };
  const products = useQuery({
    queryKey: ["products", scope, search, archived, mapping, offset],
    queryFn: ({ signal }) =>
      api<Page<Product>>(
        "/api/products?" +
          params({ ...base, search, archived, mapping, offset }),
        signal,
      ),
    enabled: ready,
  });
  const targets = useQuery({
    queryKey: ["review-targets", scope, "", 0],
    queryFn: ({ signal }) =>
      api<Page<MappingTarget>>(
        "/api/moysklad-products?" + params({ ...base, search: "", offset: 0 }),
        signal,
      ),
    enabled: ready,
  });
  const diagnostics = useQuery({
    queryKey: ["diagnostics", scope],
    queryFn: ({ signal }) =>
      api<Report>("/api/diagnostics?" + params(base), signal),
    enabled: ready,
  });
  const confirmed = diagnostics.data?.mapping_counts?.confirmed_products;
  const unconfirmed = diagnostics.data?.mapping_counts?.unconfirmed_products;
  const hasCounts = Number.isFinite(confirmed) && Number.isFinite(unconfirmed);
  const catalogTotal = diagnostics.data?.mapping_counts?.catalog_products;
  const catalogConfirmed =
    diagnostics.data?.mapping_counts?.catalog_confirmed_products;
  const hasCatalogCounts =
    typeof catalogTotal === "number" &&
    typeof catalogConfirmed === "number" &&
    Number.isInteger(catalogTotal) &&
    Number.isInteger(catalogConfirmed) &&
    catalogTotal >= 0 &&
    catalogConfirmed >= 0 &&
    catalogConfirmed <= catalogTotal;
  const targetNames = new Map(
    (targets.data?.items ?? []).map((t) => [t.kind + ":" + t.id, t]),
  );
  const reset = () => setOffset(0);
  useEffect(() => {
    setSelectedProducts([]);
    setReview(null);
    setOffset(0);
  }, [scope.tenant, scope.moy, scope.ozon]);

  return (
    <div className="product-mapping-page">
      <Link className="mapping-back" to="/integrations">
        ← Интеграции
      </Link>
      <header className="mapping-heading">
        <h1>Сопоставление товаров</h1>
        <p>
          Укажите, какому товару МойСклад соответствует товар Ozon. Каждое
          соответствие подтверждается вами.
        </p>
      </header>
      {(hasCatalogCounts || hasCounts) && (
        <section
          className="mapping-progress-card"
          aria-label="Состояние сопоставления"
        >
          <div className="mapping-progress-copy">
            <strong>
              {hasCatalogCounts
                ? `Сопоставлено ${catalogConfirmed} из ${catalogTotal}`
                : `Подтверждено соответствий: ${confirmed}`}
            </strong>
            {hasCatalogCounts && catalogTotal > 0 && (
              <div
                className="mapping-progress-bar"
                role="progressbar"
                aria-label="Сопоставление последнего каталога"
                aria-valuemin={0}
                aria-valuemax={catalogTotal}
                aria-valuenow={catalogConfirmed}
              >
                <i
                  style={{
                    width: `${(catalogConfirmed / catalogTotal) * 100}%`,
                  }}
                />
              </div>
            )}
            <p>
              {hasCatalogCounts
                ? "По последней завершённой загрузке Ozon"
                : `Без соответствия в последней загрузке: ${unconfirmed}`}
            </p>
          </div>
          <button
            className="mapping-filter-action"
            disabled={
              hasCatalogCounts
                ? catalogConfirmed === catalogTotal
                : unconfirmed === 0
            }
            onClick={() => {
              setMapping("unconfirmed");
              reset();
            }}
          >
            Проверить несопоставленные
          </button>
        </section>
      )}
      <div className="mapping-filters">
        <label>
          Поиск
          <input
            placeholder="Название, артикул, offer ID или SKU"
            value={search}
            onChange={(e) => {
              setSearch(e.target.value);
              reset();
            }}
          />
        </label>
        <label>
          Товары
          <select
            value={archived}
            onChange={(e) => {
              setArchived(e.target.value);
              reset();
            }}
          >
            <option value="">Все товары</option>
            <option value="false">Активные</option>
            <option value="true">Архивные</option>
          </select>
        </label>
        <label>
          Соответствия
          <select
            value={mapping}
            onChange={(e) => {
              setMapping(e.target.value);
              reset();
            }}
          >
            <option value="">Все соответствия</option>
            <option value="confirmed">Подтверждённые</option>
            <option value="unconfirmed">Требуют проверки</option>
          </select>
        </label>
      </div>
      {selectedProducts.length > 0 && (
        <details className="mapping-queue">
          <summary>Выбрано для проверки: {selectedProducts.length}</summary>
          <p>
            Проверьте каждую пару. Совпадение артикула не подтверждает, что это
            один товар; автоматического подтверждения нет.
          </p>
          <ul>
            {selectedProducts.map((product) => (
              <li key={product.product_id}>
                <button
                  className="text-action"
                  disabled={!!activeConfirmation.pending}
                  onClick={() => setReview(product)}
                >
                  Проверить {product.offer_id || product.product_id}
                </button>{" "}
                <span>
                  →{" "}
                  {product.candidates
                    .map(
                      (c) =>
                        `${c.name || c.id}${c.kind === "variant" ? " (вариант)" : ""}`,
                    )
                    .join(", ")}
                </span>{" "}
                <button
                  className="text-action"
                  onClick={() =>
                    setSelectedProducts((items) =>
                      items.filter(
                        (item) => item.product_id !== product.product_id,
                      ),
                    )
                  }
                >
                  Убрать
                </button>
              </li>
            ))}
          </ul>
        </details>
      )}
      {!ready ? (
        <div className="mapping-empty">
          Выберите оба подключённых аккаунта, чтобы сопоставить
          товары.
        </div>
      ) : products.isLoading ? (
        <div className="mapping-empty" role="status">
          Загружаем товары…
        </div>
      ) : products.error ? (
        <div className="mapping-empty mapping-error" role="alert">
          Не удалось загрузить товары.{" "}
          <button onClick={() => void products.refetch()}>Повторить</button>
        </div>
      ) : !products.data?.items.length ? (
        <div className="mapping-empty">
          {mapping === "confirmed" && !search && !archived && offset === 0
            ? "Для этих аккаунтов пока нет подтверждённых соответствий. Выберите товар и проверьте его перед подтверждением."
            : "По этим условиям товары не найдены."}
        </div>
      ) : (
        <section
          className="mapping-table-card"
          aria-label="Товары для сопоставления"
        >
          <div
            className="mapping-table-scroll"
            tabIndex={0}
            role="region"
            aria-label="Список товаров"
          >
            <table className="mapping-table">
              <thead>
                <tr>
                  <th>Проверить</th>
                  <th>Товар в МойСклад</th>
                  <th>Артикул</th>
                  <th>Товар в Ozon</th>
                  <th>Статус</th>
                </tr>
              </thead>
              <tbody>
                {products.data.items.map((product) => {
                  const target = product.target_id
                    ? targetNames.get(
                        product.target_kind + ":" + product.target_id,
                      )
                    : undefined;
                  const state =
                    statuses[product.match_status] ??
                    statuses.INSUFFICIENT_DATA;
                  return (
                    <tr key={product.product_id}>
                      <td data-label="Проверить">
                        {product.archived === false &&
                        product.target_id === null &&
                        product.candidates.length > 0 &&
                        !product.candidates_truncated ? (
                          <input
                            type="checkbox"
                            aria-label={`Выбрать ${product.offer_id || product.product_id} для проверки`}
                            checked={selectedProducts.some(
                              (item) => item.product_id === product.product_id,
                            )}
                            disabled={
                              selectedProducts.length >= 100 &&
                              !selectedProducts.some(
                                (item) =>
                                  item.product_id === product.product_id,
                              )
                            }
                            onChange={(e) =>
                              setSelectedProducts((items) =>
                                e.target.checked
                                  ? [...items, product]
                                  : items.filter(
                                      (item) =>
                                        item.product_id !== product.product_id,
                                    ),
                              )
                            }
                          />
                        ) : (
                          <span aria-label="Нет предложений для групповой проверки">
                            —
                          </span>
                        )}
                      </td>
                      <td data-label="Товар в МойСклад">
                        <button
                          className="mapping-select"
                          aria-label={`Сопоставить ${product.offer_id || product.product_id}`}
                          onClick={() => setReview(product)}
                        >
                          <span>
                            {target?.name ||
                              (product.target_id
                                ? "Товар МойСклад выбран"
                                : "Выберите товар МойСклад")}
                          </span>
                          <span aria-hidden="true">⌄</span>
                        </button>
                        {product.candidate_count > 0 && !product.target_id && (
                          <small>Предложений: {product.candidate_count}</small>
                        )}
                      </td>
                      <td className="mapping-article" data-label="Артикул">
                        {target?.article || "—"}
                      </td>
                      <td data-label="Товар в Ozon">
                        <strong>
                          {product.offer_id || `Товар ${product.product_id}`}
                        </strong>
                        {product.sku && <small>SKU: {product.sku}</small>}
                        {product.archived === true && <small>В архиве</small>}
                      </td>
                      <td data-label="Статус">
                        <span className={`mapping-chip ${state.tone}`}>
                          {state.label}
                        </span>
                        {product.candidates_truncated && (
                          <small>Часть предложений недоступна</small>
                        )}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
          <footer className="mapping-table-footer">
            <span>
              Показаны {offset + 1}–{offset + products.data.items.length}
            </span>
            <div>
              <button
                disabled={offset === 0}
                onClick={() => setOffset(Math.max(0, offset - 50))}
              >
                Назад
              </button>
              <button
                disabled={!products.data.has_next}
                onClick={() => setOffset(offset + 50)}
              >
                Далее
              </button>
            </div>
          </footer>
        </section>
      )}
      {review && (
        <ProductReview
          key={review.product_id}
          product={
            products.data?.items.find(
              (item) => item.product_id === review.product_id,
            ) ?? review
          }
          scope={scope}
          close={() => {
            if (
              activeConfirmation.result &&
              activeConfirmation.resultContext?.product === review.product_id
            ) {
              setSelectedProducts((items) =>
                items.filter((item) => item.product_id !== review.product_id),
              );
            }
            setReview(null);
          }}
          confirmation={activeConfirmation}
        />
      )}
    </div>
  );
}
