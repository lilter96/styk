import type { CatalogConfig } from "./CatalogSyncTypes";
import { Icon } from "./UI";
import { useEffect, useRef, useState } from "react";
import {
  ProductMappingTable,
  MappingPagination,
  eligibleForQueue,
} from "./ProductMappingTable";
import { useQuery } from "@tanstack/react-query";
import { api, params, type Page, type Product } from "./api";
import { useConfirmation, type Confirmation, type Scope } from "./Confirmation";
import { Link } from "react-router-dom";
import { ProductReverseMatching } from "./ProductReverseMatching";
import { ProductImportMenu } from "./ProductImportMenu";
import { ProductDetail } from "./ProductDetail";
import { ProductReview } from "./ProductReview";

export function ProductMappingPage({
  scope,
  confirmation,
  config,
}: {
  scope: Scope;
  confirmation?: Confirmation;
  config?: CatalogConfig;
}) {
  const localConfirmation = useConfirmation();
  const activeConfirmation = confirmation ?? localConfirmation;
  const skipped = useRef(new Set<string>());
  const [queueMessage, setQueueMessage] = useState("");
  const queueLock = useRef(false);
  const queueRequest = useRef<AbortController | null>(null);
  const [detail, setDetail] = useState<Product | null>(null);
  const [review, setReview] = useState<Product | null>(null);
  const [search, setSearch] = useState("");
  const [query, setQuery] = useState("");
  const [pageSize, setPageSize] = useState(20);
  useEffect(() => {
    if (search === query) return;
    const timer = setTimeout(() => {
      setQuery(search);
      setOffset(0);
    }, 350);
    return () => clearTimeout(timer);
  }, [search, query]);
  const [filtersOpen, setFiltersOpen] = useState(false);
  const [archived, setArchived] = useState("");
  const [provider, setProvider] = useState("");
  const [mapping, setMapping] = useState("");
  const [offset, setOffset] = useState(0);
  const [selectedProducts, setSelectedProducts] = useState<Product[]>([]);
  const [completed, setCompleted] = useState<string[]>([]);
  const ready = !!(scope.tenant && scope.moy && scope.ozon);
  const base = {
    tenant: scope.tenant,
    moysklad_account: scope.moy,
    ozon_account: scope.ozon,
  };
  const products = useQuery({
    queryKey: [
      "products",
      scope,
      query,
      archived,
      mapping,
      provider,
      offset,
      pageSize,
    ],
    queryFn: ({ signal }) =>
      api<Page<Product> & { total?: number }>(
        "/api/products?" +
          params({
            ...base,
            search: query,
            archived,
            mapping,
            provider,
            offset,
            pageSize,
          }),
        signal,
      ),
    enabled: ready,
  });
  useEffect(() => {
    if (!products.data) return;
    setSelectedProducts((previous) =>
      previous.map(
        (item) =>
          products.data.items.find(
            (p) =>
              p.product_id === item.product_id && p.provider === item.provider,
          ) || item,
      ),
    );
  }, [products.data]);
  const reset = () => setOffset(0);
  useEffect(() => {
    queueRequest.current?.abort();
    skipped.current.clear();
    setQueueMessage("");
    setSelectedProducts([]);
    setCompleted([]);
    setReview(null);
    setDetail(null);
    setOffset(0);
    return () => {
      queueRequest.current?.abort();
    };
  }, [scope.tenant, scope.moy, scope.ozon]);

  useEffect(() => {
    const context = activeConfirmation.resultContext;
    if (
      !activeConfirmation.result ||
      context?.intent.kind !== "product" ||
      context.scope.tenant !== scope.tenant ||
      context.scope.moy !== scope.moy ||
      context.scope.ozon !== scope.ozon ||
      !selectedProducts.some(
        (product) => product.product_id === context.product,
      )
    )
      return;
    setCompleted((items) =>
      items.includes(context.product) ? items : [...items, context.product],
    );
  }, [
    activeConfirmation.result,
    activeConfirmation.resultContext,
    scope.tenant,
    scope.moy,
    scope.ozon,
    selectedProducts,
  ]);

  async function nextProduct() {
    if (queueLock.current || !review) return;
    queueLock.current = true;
    const request = new AbortController();
    queueRequest.current = request;
    skipped.current.add(review.product_id);
    setQueueMessage("Загружаем следующий товар…");
    try {
      for (let start = 0; start <= 100000; start += 50) {
        const page = await api<Page<Product>>(
          "/api/products?" +
            params({
              ...base,
              mapping: "unconfirmed",
              provider: "ozon",
              archived: "false",
              offset: start,
            }),
          request.signal,
        );
        if (request.signal.aborted) return;
        const item = page.items.find(
          (item) => !skipped.current.has(item.product_id),
        );
        if (item) {
          setReview(item);
          setQueueMessage("");
          return;
        }
        if (!page.has_next) {
          setReview(null);
          setQueueMessage(
            "Очередь завершена. Несопоставленных товаров больше нет.",
          );
          return;
        }
      }
      setQueueMessage("Достигнут предел очереди. Уточните фильтры каталога.");
    } catch {
      if (request.signal.aborted) return;
      setQueueMessage(
        "Не удалось загрузить следующий товар. Попробуйте ещё раз.",
      );
    } finally {
      queueLock.current = false;
    }
  }
  return (
    <div className="product-mapping-page">
      <div className={`mapping-filters ${filtersOpen ? "is-filtering" : ""}`}>
        <div className="products-mobile-import">
          <ProductImportMenu />
        </div>
        <label className="products-source-filter">
          Источник
          <select
            aria-label="Источник товара"
            value={provider}
            onChange={(e) => {
              setProvider(e.target.value);
              reset();
            }}
          >
            <option value="">Все товары</option>
            <option value="ozon">Предложения Ozon</option>
            <option value="moysklad">Только в МойСклад</option>
          </select>
        </label>
        <label className="matching-page-search">
          <span className="matching-page-sr">Поиск</span>
          <div>
            <Icon name="search" size="button" />
            <input
              aria-label="Поиск"
              placeholder="Поиск по названию или артикулу"
              value={search}
              onChange={(e) => {
                setSearch(e.target.value);
                reset();
              }}
            />
            {(products.isFetching || search !== query) && (
              <span className="matching-page-spinner" aria-label="Поиск" />
            )}
            {search && (
              <button
                aria-label="Очистить поиск"
                onClick={() => {
                  setSearch("");
                  reset();
                }}
              >
                <Icon name="close" size="button" />
              </button>
            )}
          </div>
        </label>
        <label className="products-lifecycle-filter">
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
        <Link
          className="secondary products-mobile-matching"
          to="/products/matching"
        >
          Автосопоставление
        </Link>
        <div className="products-overview-pills" aria-label="Фильтр связей">
          {[
            ["", "Все"],
            ["confirmed", "Связанные"],
            ["unconfirmed", "Без пары"],
            ["conflict", "Конфликты"],
          ].map(([value, label]) => (
            <button
              key={value}
              className={mapping === value ? "active" : "secondary"}
              aria-pressed={mapping === value}
              onClick={() => {
                setMapping(value);
                reset();
              }}
            >
              {label}
            </button>
          ))}
        </div>
        <button
          className="secondary products-mobile-filters"
          aria-label="Фильтры"
          aria-expanded={filtersOpen}
          onClick={() => setFiltersOpen(!filtersOpen)}
        >
          <Icon name="settings" size="button" />
          <span>Фильтры</span>
        </button>
        <button
          className="matching-page-reset"
          disabled={!search && !archived && !mapping && !provider}
          onClick={() => {
            setSearch("");
            setQuery("");
            setArchived("");
            setMapping("");
            setProvider("");
            reset();
          }}
        >
          Сбросить
        </button>
      </div>
      {selectedProducts.length > 0 && (
        <details className="mapping-queue">
          <summary>Выбрано для проверки: {selectedProducts.length}</summary>
          <p>
            Проверьте каждую пару. Совпадение артикула не подтверждает, что это
            один товар; автоматического подтверждения нет.
          </p>
          <p role="status">
            Подтверждено: {completed.length} · Осталось проверить:{" "}
            {selectedProducts.length - completed.length}
          </p>
          <ul>
            {selectedProducts.map((product) => (
              <li key={product.product_id}>
                {completed.includes(product.product_id) ? (
                  <strong>
                    Подтверждено: {product.offer_id || product.product_id}
                  </strong>
                ) : (
                  <button
                    className="text-action"
                    disabled={!!activeConfirmation.pending}
                    onClick={() => setReview(product)}
                  >
                    Проверить {product.offer_id || product.product_id}
                  </button>
                )}{" "}
                {!completed.includes(product.product_id) && (
                  <span>
                    Предложения:{" "}
                    {product.candidates
                      .map(
                        (c) =>
                          `${c.name || c.id}${c.kind === "variant" ? " (вариант)" : ""}`,
                      )
                      .join(", ")}
                  </span>
                )}{" "}
                <button
                  className="text-action"
                  aria-label={`Убрать ${product.offer_id || product.product_id} из очереди`}
                  onClick={() => {
                    setSelectedProducts((items) =>
                      items.filter(
                        (item) => item.product_id !== product.product_id,
                      ),
                    );
                    setCompleted((items) =>
                      items.filter((id) => id !== product.product_id),
                    );
                  }}
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
          Выберите оба подключённых аккаунта, чтобы сопоставить товары.
        </div>
      ) : products.isLoading ? (
        <div
          className="matching-page-skeleton"
          role="status"
          aria-label="Загружаем товары"
        >
          {Array.from({ length: 5 }, (_, i) => (
            <div key={i} />
          ))}
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
            : mapping === "unconfirmed" && !search && !archived
              ? "Все товары сопоставлены. Несопоставленных товаров нет."
              : !search && !archived && !mapping
                ? "Каталог пока пуст. Загрузите товары в разделе интеграций."
                : "По этим условиям товары не найдены."}
          {offset > 0 && (
            <p>
              <button onClick={() => setOffset(Math.max(0, offset - pageSize))}>
                Назад к предыдущим товарам
              </button>
            </p>
          )}
        </div>
      ) : (
        <ProductMappingTable
          items={products.data.items}
          config={config}
          selected={selectedProducts}
          pending={!!activeConfirmation.pending}
          open={(p) =>
            p.match_status === "CONFIRMED" ? setDetail(p) : setReview(p)
          }
          toggle={(product, checked) => {
            setSelectedProducts((items) =>
              checked
                ? [...items, product]
                : items.filter(
                    (item) => item.product_id !== product.product_id,
                  ),
            );
            if (!checked)
              setCompleted((items) =>
                items.filter((id) => id !== product.product_id),
              );
          }}
          selectPage={(checked) => {
            const pageIds = new Set(
              products.data.items
                .filter(eligibleForQueue)
                .map((p) => p.product_id),
            );
            if (!checked) {
              setSelectedProducts((items) =>
                items.filter((p) => !pageIds.has(p.product_id)),
              );
              setCompleted((items) => items.filter((id) => !pageIds.has(id)));
              return;
            }
            setSelectedProducts((items) => [
              ...items,
              ...products.data.items
                .filter(
                  (p) =>
                    eligibleForQueue(p) &&
                    !items.some((item) => item.product_id === p.product_id),
                )
                .slice(0, 100 - items.length),
            ]);
          }}
          startQueue={() => {
            const product = selectedProducts.find(
              (p) =>
                p.match_status !== "CONFIRMED" &&
                !completed.includes(p.product_id),
            );
            if (product && product.match_status !== "CONFIRMED")
              setReview(product);
          }}
          clearSelection={() => {
            setSelectedProducts([]);
            setCompleted([]);
          }}
          canStartQueue={selectedProducts.some(
            (p) =>
              p.match_status !== "CONFIRMED" &&
              !completed.includes(p.product_id),
          )}
        >
          <MappingPagination
            offset={offset}
            size={pageSize}
            total={products.data.total}
            length={products.data.items.length}
            hasNext={products.data.has_next}
            busy={products.isFetching}
            setOffset={setOffset}
            setSize={(size) => {
              setPageSize(size);
              reset();
            }}
          />
        </ProductMappingTable>
      )}
      {selectedProducts.some((p) => p.match_status === "CONFIRMED") && (
        <div className="products-selection-footer">
          <span>
            <b>
              Связанных товаров:{" "}
              {
                selectedProducts.filter((p) => p.match_status === "CONFIRMED")
                  .length
              }
            </b>{" "}
            · Проверка ничего не отправляет
          </span>
          <Link
            className="primary-button"
            to="/products/transfer"
            state={{
              scope,
              products: selectedProducts
                .filter((p) => p.match_status === "CONFIRMED")
                .map((p) => ({ id: p.product_id, offer: p.offer_id || "" })),
            }}
          >
            Проверить изменения (
            {
              selectedProducts.filter((p) => p.match_status === "CONFIRMED")
                .length
            }
            ) →
          </Link>
        </div>
      )}
      {detail && (
        <ProductDetail
          key={`${detail.provider}:${detail.product_id}`}
          product={detail}
          scope={scope}
          config={config}
          onClose={() => setDetail(null)}
          onMatch={() => {
            setReview(detail);
            setDetail(null);
          }}
        />
      )}
      {queueMessage && <p role="status">{queueMessage}</p>}
      {review?.provider === "moysklad" && (
        <ProductReverseMatching
          key={review.product_id}
          product={review}
          scope={scope}
          confirmation={activeConfirmation}
          close={() => setReview(null)}
        />
      )}
      {review && review.provider !== "moysklad" && (
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
              activeConfirmation.resultContext?.intent.kind === "product" &&
              activeConfirmation.resultContext.scope.tenant === scope.tenant &&
              activeConfirmation.resultContext.scope.moy === scope.moy &&
              activeConfirmation.resultContext.scope.ozon === scope.ozon &&
              activeConfirmation.resultContext.product === review.product_id
            ) {
              if (
                selectedProducts.some(
                  (item) => item.product_id === review.product_id,
                )
              ) {
                setCompleted((items) =>
                  items.includes(review.product_id)
                    ? items
                    : [...items, review.product_id],
                );
              }
            }
            setReview(null);
          }}
          confirmation={activeConfirmation}
          next={() => void nextProduct()}
        />
      )}
    </div>
  );
}
