import { useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { api, ApiError, post } from "./api";
import { BrandLogo } from "./BrandLogo";
import { IntegrationDialog } from "./IntegrationDialog";
import { CatalogSyncCreation, newCatalogCreation } from "./CatalogSyncCreation";
import { CatalogSyncFacts } from "./CatalogSyncFacts";
import { CatalogSyncPreview } from "./CatalogSyncPreview";
import {
  catalogURL,
  scopeBody,
  productName,
  requestError,
  normalizePreview,
  type CatalogProduct,
  type CatalogPreview,
  type CatalogOperation,
  type Enrollment,
  type ProductFacts,
  type ProductPage,
  type Provider,
  type Scope,
} from "./CatalogSyncTypes";

type Selection = {
  product: CatalogProduct;
  enrollment: Enrollment;
  facts?: ProductFacts;
};
const identityKey = (p: CatalogProduct) =>
  `${p.identity.entity_type}:${p.identity.id}`;
const linked = (p: CatalogProduct, data?: ProductPage) =>
  data?.enrollments?.some((e) =>
    [e.source, e.target].some(
      (i) =>
        i.account.provider === p.identity.account.provider &&
        i.id === p.identity.id &&
        (!i.entity_type || i.entity_type === p.identity.entity_type),
    ),
  );

export function ProductImport({
  scope,
  provider,
}: {
  scope: Scope;
  provider: Provider;
}) {
  const client = useQueryClient();
  const [search, setSearch] = useState("");
  const [cursor, setCursor] = useState("");
  const [pages, setPages] = useState<string[]>([]);
  const [selected, setSelected] = useState<Record<string, Selection>>({});
  const [editing, setEditing] = useState<string | null>(null);
  const [preview, setPreview] = useState<CatalogPreview | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [operation, setOperation] = useState<CatalogOperation | null>(null);
  const [uncertain, setUncertain] = useState(false);
  const controller = useRef(new AbortController());
  const lock = useRef(false);
  const approvalKey = useRef(crypto.randomUUID());
  useEffect(() => {
    const active = new AbortController();
    controller.current = active;
    return () => active.abort();
  }, []);
  const products = useQuery({
    queryKey: ["catalog-products", scope, provider, search, cursor],
    queryFn: ({ signal }) =>
      api<ProductPage>(
        catalogURL("products", scope, {
          provider,
          search,
          cursor,
          limit: "20",
        }),
        signal,
      ),
    retry: false,
  });
  function add(product: CatalogProduct): Selection {
    const enrollment = newCatalogCreation(product, scope);
    if (provider === "ozon" && product.identity.offer_id)
      enrollment.create!.values.article = {
        state: "known",
        data: product.identity.offer_id,
      };
    return { product, enrollment };
  }
  function toggle(product: CatalogProduct) {
    setPreview(null);
    const key = identityKey(product);
    setSelected((previous) => {
      const next = { ...previous };
      if (next[key]) delete next[key];
      else if (Object.keys(next).length < 20) next[key] = add(product);
      return next;
    });
  }
  async function prepare() {
    if (lock.current || !Object.keys(selected).length || uncertain) return;
    lock.current = true;
    setBusy(true);
    setError("");
    setPreview(null);
    try {
      const result = await post<CatalogPreview>(
        "/api/catalog-sync/import-preview",
        {
          ...scopeBody(scope),
          targets: Object.values(selected).map((s) => s.enrollment),
        },
        AbortSignal.any([
          controller.current.signal,
          AbortSignal.timeout(120000),
        ]),
      );
      if (controller.current.signal.aborted) return;
      if (
        result.scope.tenant !== scope.tenant ||
        result.scope.moysklad_account !== scope.moy ||
        result.scope.ozon_account !== scope.ozon
      )
        throw new Error("scope mismatch");
      setPreview(normalizePreview(result));
      approvalKey.current = crypto.randomUUID();
      await client.invalidateQueries({ queryKey: ["catalog-config", scope] });
    } catch (error) {
      if (!controller.current.signal.aborted) setError(requestError(error));
    } finally {
      lock.current = false;
      setBusy(false);
    }
  }
  async function approve(ids: string[]) {
    if (lock.current || !preview || uncertain) return;
    lock.current = true;
    setBusy(true);
    setError("");
    try {
      const result = await post<CatalogOperation>(
        "/api/catalog-sync/approve",
        {
          ...scopeBody(scope),
          preview_id: preview.id,
          enrollment_ids: ids,
          acknowledge: true,
          idempotency_key: approvalKey.current,
        },
        AbortSignal.any([
          controller.current.signal,
          AbortSignal.timeout(45000),
        ]),
      );
      if (controller.current.signal.aborted) return;
      if (
        !result.id ||
        result.scope.tenant !== scope.tenant ||
        result.scope.ozon_account !== scope.ozon ||
        result.scope.moysklad_account !== scope.moy
      )
        throw new Error("scope mismatch");
      setOperation(result);
      setPreview(null);
      setSelected({});
      await client.invalidateQueries({
        queryKey: ["catalog-operations", scope],
      });
    } catch (error) {
      if (
        error instanceof ApiError &&
        [400, 403, 409, 410, 422].includes(error.status)
      ) {
        setPreview(null);
        setError(requestError(error));
      } else {
        setUncertain(true);
        setError(
          "Ответ на подтверждение не получен. Операция могла сохраниться. Проверьте историю отправок перед новым импортом.",
        );
      }
    } finally {
      lock.current = false;
      setBusy(false);
    }
  }
  const items = products.data?.products || [];
  const eligible = items.filter(
    (p) => !linked(p, products.data) && p.lifecycle === "active" && !p.variant,
  );
  const edit = editing ? selected[editing] : undefined;
  return (
    <div className="catalog-sync products-import-flow">
      <ol className="products-steps">
        <li>Выбор товаров</li>
        <li aria-current={preview ? "step" : undefined}>Проверка данных</li>
        <li aria-current={operation ? "step" : undefined}>
          Подтверждение и сверка
        </li>
      </ol>
      <p className="products-notice">
        Одноразовый импорт создаёт выбранные карточки. Правила и разрешения
        автоматической передачи сохраняются.
      </p>
      {operation ? (
        <section className="catalog-section">
          <h2>Запрос поставлен в обработку</h2>
          <p>
            Товаров в очереди: {operation.targets.length}. Создание будет
            подтверждено после сверки с сервисом.
          </p>
          <Link className="primary-button" to="/products/history">
            Посмотреть результат
          </Link>
        </section>
      ) : (
        <>
          <section className="catalog-section">
            <div className="catalog-section-heading">
              <h2>Выберите товары для создания</h2>
              <span>До 20 товаров за одно подтверждение</span>
            </div>
            {products.data?.summary && (
              <div className="products-import-summary">
                <p>
                  Найдено: <b>{products.data.summary.total}</b> · К проверке:{" "}
                  <b>{products.data.summary.to_review}</b> · Уже связаны:{" "}
                  <b>{products.data.summary.linked}</b> · Исключены:{" "}
                  <b>{products.data.summary.excluded}</b>
                </p>
                <small>
                  {products.data.summary.observed_at
                    ? `Полная загрузка: ${new Date(products.data.summary.observed_at).toLocaleString("ru-RU")}`
                    : "Загруженного каталога пока нет"}
                  . Готовность к созданию проверим перед отправкой.
                </small>
              </div>
            )}
            <label className="products-import-search">
              Поиск по названию или артикулу
              <input
                type="search"
                value={search}
                onChange={(e) => {
                  setSearch(e.target.value);
                  setCursor("");
                  setPages([]);
                }}
              />
            </label>
            {products.isFetching && <p role="status">Загружаем товары…</p>}
            {products.isError && (
              <p role="alert" className="catalog-error">
                {requestError(products.error)}{" "}
                <button
                  className="secondary"
                  onClick={() => void products.refetch()}
                >
                  Повторить
                </button>
              </p>
            )}
            <div className="products-import-selection">
              <button
                className="secondary"
                disabled={busy || !eligible.length || uncertain}
                onClick={() => {
                  setPreview(null);
                  setSelected({
                    ...selected,
                    ...Object.fromEntries(
                      eligible
                        .filter((p) => !selected[identityKey(p)])
                        .slice(0, 20 - Object.keys(selected).length)
                        .map((p) => [identityKey(p), add(p)]),
                    ),
                  });
                }}
              >
                Выбрать доступные на странице
              </button>
              <span>Выбрано: {Object.keys(selected).length}</span>
            </div>
            <div className="products-import-list">
              {items.map((product) => {
                const key = identityKey(product),
                  paired = linked(product, products.data),
                  chosen = selected[key];
                return (
                  <article key={key} className={chosen ? "is-selected" : ""}>
                    <label className="products-select-control">
                      <input
                        type="checkbox"
                        aria-label={`Выбрать ${productName(product)}`}
                        checked={!!chosen}
                        disabled={
                          busy ||
                          uncertain ||
                          paired ||
                          product.lifecycle !== "active" ||
                          product.variant ||
                          (!chosen && Object.keys(selected).length >= 20)
                        }
                        onChange={() => toggle(product)}
                      />
                    </label>
                    <BrandLogo provider={provider} decorative />
                    <div>
                      <strong>{productName(product)}</strong>
                      <small>
                        {product.identity.offer_id ||
                          (product.fields.article?.state === "known"
                            ? String(product.fields.article.data)
                            : "Артикул не указан")}
                        {product.variant ? " · Вариант товара" : ""}
                      </small>
                    </div>
                    <span
                      className={`products-import-state ${paired ? "linked" : ""}`}
                    >
                      {paired
                        ? "Уже связан"
                        : product.variant
                          ? "Модификация: требуется отдельная проверка"
                          : product.lifecycle === "unknown"
                            ? "Активность неизвестна: обновите каталог"
                            : product.lifecycle === "archived"
                              ? "В архиве"
                              : provider === "moysklad"
                                ? "Нужно проверить поля Ozon"
                                : "Нужна проверка перед созданием"}
                    </span>
                    {chosen && (
                      <button
                        className="secondary"
                        disabled={busy || uncertain}
                        onClick={() => setEditing(key)}
                      >
                        Заполнить данные
                      </button>
                    )}
                  </article>
                );
              })}
            </div>
            {!products.isFetching && !products.isError && !items.length && (
              <p className="catalog-empty">
                Товары не найдены. Измените поиск или обновите каталог в
                интеграциях.
              </p>
            )}
            <div className="catalog-actions">
              <button
                className="secondary"
                disabled={!pages.length || products.isFetching}
                onClick={() => {
                  setCursor(pages.at(-1) || "");
                  setPages(pages.slice(0, -1));
                }}
              >
                Назад
              </button>
              <span>Страница {pages.length + 1}</span>
              <button
                className="secondary"
                disabled={!products.data?.next_cursor || products.isFetching}
                onClick={() => {
                  setPages([...pages, cursor]);
                  setCursor(products.data!.next_cursor!);
                }}
              >
                Далее
              </button>
            </div>
            <div className="products-import-footer">
              <p>
                Проверка прочитает актуальные данные и требования сервиса.
                Отправки ещё не будет.
              </p>
              <button
                disabled={busy || uncertain || !Object.keys(selected).length}
                onClick={() => void prepare()}
              >
                {busy
                  ? "Проверяем…"
                  : `Проверить выбранные (${Object.keys(selected).length})`}
              </button>
            </div>
          </section>
          {preview && (
            <CatalogSyncPreview
              key={preview.id}
              preview={preview}
              busy={busy}
              blocked={uncertain}
              onApprove={(ids) => void approve(ids)}
            />
          )}
        </>
      )}
      {error && (
        <p role="alert" className="catalog-error">
          {error}{" "}
          {uncertain && <Link to="/products/history">История отправок</Link>}
        </p>
      )}
      {edit && editing && (
        <IntegrationDialog
          title={productName(edit.product)}
          className="products-create-dialog"
          onClose={() => setEditing(null)}
        >
          <CatalogSyncCreation
            enrollment={edit.enrollment}
            onChange={(enrollment) => {
              setSelected({ ...selected, [editing]: { ...edit, enrollment } });
              setPreview(null);
            }}
          />
          {provider === "moysklad" && (
            <CatalogSyncFacts
              scope={scope}
              facts={edit.facts}
              onChange={(facts) => {
                const create = edit.enrollment.create!;
                const values = { ...create.values };
                delete values.category;
                delete values.attributes;
                setSelected({
                  ...selected,
                  [editing]: {
                    ...edit,
                    facts,
                    enrollment: {
                      ...edit.enrollment,
                      create: {
                        ...create,
                        values: { ...values, ...facts.values },
                      },
                    },
                  },
                });
                setPreview(null);
              }}
            />
          )}
          <button onClick={() => setEditing(null)}>
            Сохранить данные для проверки
          </button>
        </IntegrationDialog>
      )}
    </div>
  );
}
