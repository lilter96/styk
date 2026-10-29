import { useState, type FormEvent } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "./api";
import { CatalogSyncFacts } from "./CatalogSyncFacts";
import { CatalogSyncCreation, newCatalogCreation } from "./CatalogSyncCreation";
import {
  catalogURL,
  fieldLabel,
  productName,
  providerLabel,
  requestError,
  type CatalogConfig,
  type CatalogValue,
  type ProductFacts,
  type ProductPage,
  type CatalogProduct,
  type Enrollment,
  type Identity,
  type Provider,
  type Scope,
} from "./CatalogSyncTypes";

const sameIdentity = (left: Identity, right: Identity) =>
  left.id === right.id &&
  left.account.id === right.account.id &&
  left.account.provider === right.account.provider;

export type ProductSelection = {
  product: CatalogProduct;
  enrollment: Enrollment;
  manual_values: Record<string, CatalogValue>;
  facts?: ProductFacts;
};

export function CatalogSyncProducts({
  scope,
  config,
  selected,
  onChange,
  disabled,
}: {
  scope: Scope;
  config: CatalogConfig;
  selected: Record<string, ProductSelection>;
  onChange: (selected: Record<string, ProductSelection>) => void;
  disabled: boolean;
}) {
  const [provider, setProvider] = useState<Provider>("moysklad");
  const [draft, setDraft] = useState("");
  const [search, setSearch] = useState("");
  const [cursor, setCursor] = useState("");
  const [history, setHistory] = useState<string[]>([]);
  const query = useQuery({
    queryKey: ["catalog-products", scope, provider, search, cursor],
    queryFn: async ({ signal }) => {
      const value = await api<ProductPage>(
        catalogURL("products", scope, {
          provider,
          search,
          cursor,
          limit: "20",
        }),
        signal,
      );
      return { ...value, products: value.products || [] };
    },
    retry: false,
  });
  function find(event: FormEvent) {
    event.preventDefault();
    setSearch(draft.trim());
    setCursor("");
    setHistory([]);
  }
  function toggle(product: CatalogProduct, enrollment: Enrollment) {
    const next = { ...selected };
    if (next[enrollment.id]) delete next[enrollment.id];
    else next[enrollment.id] = { product, enrollment, manual_values: {} };
    onChange(next);
  }
  const manualFields = Object.entries(config.rules)
    .filter(
      ([field, value]) =>
        value === "MANUAL" && field !== "category" && field !== "attributes",
    )
    .map(([field]) => field);
  return (
    <section
      className="catalog-section"
      aria-labelledby="catalog-products-title"
    >
      <h2 id="catalog-products-title">Товары для синхронизации</h2>
      <p>
        Выберите конкретные товары. Выбор сохраняется при переходе между
        страницами. Список товаров не меняет источники, заданные для полей.
      </p>
      <form className="catalog-search" onSubmit={find}>
        <label>
          Список товаров
          <select
            value={provider}
            onChange={(event) => {
              setProvider(event.target.value as Provider);
              setCursor("");
              setHistory([]);
            }}
          >
            <option value="moysklad">МойСклад</option>
            <option value="ozon">Ozon</option>
          </select>
        </label>
        <label>
          Поиск товаров
          <input
            type="search"
            placeholder="Название или артикул"
            value={draft}
            onChange={(event) => setDraft(event.target.value)}
          />
        </label>
        <button className="secondary">Найти товары</button>
      </form>
      {query.isFetching && <p role="status">Загружаем товары…</p>}
      {query.isError && (
        <p className="catalog-error" role="alert">
          {requestError(query.error)}{" "}
          <button
            type="button"
            className="text-action"
            onClick={() => void query.refetch()}
          >
            Повторить чтение товаров
          </button>
        </p>
      )}
      {query.data && (
        <>
          {query.data.products.length ? (
            <div
              className="catalog-table-scroll"
              tabIndex={0}
              aria-label="Товары на текущей странице"
            >
              <table className="catalog-table">
                <thead>
                  <tr>
                    <th scope="col">Выбор</th>
                    <th scope="col">Товар в источнике</th>
                    <th scope="col">Товар во втором сервисе</th>
                    <th scope="col">Соответствие</th>
                  </tr>
                </thead>
                <tbody>
                  {query.data.products.map((product) => {
                    const candidate = [
                      ...(query.data.enrollments || []),
                      ...config.targets,
                      ...Object.values(selected).map(
                        (selection) => selection.enrollment,
                      ),
                    ].find(
                      (item) =>
                        sameIdentity(item.source, product.identity) ||
                        sameIdentity(item.target, product.identity),
                    );
                    const saved =
                      candidate &&
                      config.targets.find((item) => item.id === candidate.id);
                    const enrollment =
                      saved &&
                      candidate &&
                      saved.mapping_id === candidate.mapping_id &&
                      saved.mapping_version === candidate.mapping_version &&
                      sameIdentity(saved.source, candidate.source) &&
                      sameIdentity(saved.target, candidate.target)
                        ? saved
                        : candidate;
                    const paired = enrollment
                      ? sameIdentity(enrollment.source, product.identity)
                        ? enrollment.target
                        : enrollment.source
                      : null;
                    return (
                      <tr
                        key={`${product.identity.account.provider}:${product.identity.id}`}
                      >
                        <td>
                          <input
                            type="checkbox"
                            aria-label={`Выбрать товар ${productName(product)}`}
                            checked={!!enrollment && !!selected[enrollment.id]}
                            disabled={
                              disabled ||
                              !enrollment ||
                              product.lifecycle === "archived"
                            }
                            onChange={() =>
                              enrollment && toggle(product, enrollment)
                            }
                          />
                        </td>
                        <td>
                          <strong>{productName(product)}</strong>
                          <small>
                            {providerLabel(product.identity.account.provider)}:{" "}
                            {product.identity.offer_id || product.identity.id}
                          </small>
                          {product.variant && (
                            <small>
                              Вариант товара
                              {product.parent ? ` · ${product.parent.id}` : ""}
                            </small>
                          )}
                        </td>
                        <td>
                          {enrollment && paired ? (
                            <>
                              <strong>
                                {providerLabel(paired.account.provider)}
                              </strong>
                              <small>
                                {paired.offer_id ||
                                  paired.id ||
                                  (enrollment.create
                                    ? "Создание нового товара требует проверки"
                                    : "Нет данных")}
                              </small>
                            </>
                          ) : (
                            "Соответствующий товар не найден"
                          )}
                        </td>
                        <td>
                          {product.lifecycle === "archived"
                            ? "Товар в архиве"
                            : enrollment?.mapping_id
                              ? "Подтверждено"
                              : enrollment?.create
                                ? "Создание по явной настройке"
                                : "Нет подтверждённого соответствия"}
                          {!enrollment && product.lifecycle !== "archived" && (
                            <button
                              type="button"
                              className="secondary"
                              disabled={disabled}
                              onClick={() => {
                                const created = newCatalogCreation(
                                  product,
                                  scope,
                                );
                                onChange({
                                  ...selected,
                                  [created.id]: {
                                    product,
                                    enrollment: created,
                                    manual_values: {},
                                  },
                                });
                              }}
                            >
                              Создать в{" "}
                              {product.identity.account.provider === "ozon"
                                ? "МойСклад"
                                : "Ozon"}
                            </button>
                          )}
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          ) : (
            <p className="catalog-empty">
              Товары не найдены. Измените поиск или загрузите каталог в разделе
              интеграций.
            </p>
          )}
          <div className="catalog-actions">
            <button
              type="button"
              className="secondary"
              disabled={!history.length || query.isFetching}
              onClick={() => {
                setCursor(history[history.length - 1]);
                setHistory(history.slice(0, -1));
              }}
            >
              Предыдущие товары
            </button>
            <span>
              Страница {history.length + 1}
              {query.data.total != null
                ? ` · Всего товаров: ${query.data.total}`
                : ""}
            </span>
            <button
              type="button"
              className="secondary"
              disabled={!query.data.next_cursor || query.isFetching}
              onClick={() => {
                setHistory([...history, cursor]);
                setCursor(query.data!.next_cursor!);
              }}
            >
              Следующие товары
            </button>
          </div>
        </>
      )}
      <div className="catalog-selection" aria-live="polite">
        Выбрано товаров: {Object.keys(selected).length}
      </div>
      {Object.entries(selected).map(([id, selection]) => (
        <article key={id} className="catalog-selected-product">
          <header>
            <h3>{productName(selection.product)}</h3>
            <button
              type="button"
              className="text-action"
              disabled={disabled}
              onClick={() => toggle(selection.product, selection.enrollment)}
            >
              Убрать {productName(selection.product)} из выбора
            </button>
          </header>
          <fieldset disabled={disabled}>
            <legend className="catalog-sr-only">
              Данные товара {productName(selection.product)}
            </legend>
            {selection.enrollment.create && (
              <CatalogSyncCreation
                enrollment={selection.enrollment}
                onChange={(enrollment) =>
                  onChange({ ...selected, [id]: { ...selection, enrollment } })
                }
              />
            )}
            {!selection.enrollment.create &&
              manualFields.map((field) => (
                <label key={field}>
                  Вручную: {fieldLabel(field)}
                  <input
                    value={
                      typeof selection.manual_values[field]?.data === "string"
                        ? (selection.manual_values[field].data as string)
                        : ""
                    }
                    onChange={(event) =>
                      onChange({
                        ...selected,
                        [id]: {
                          ...selection,
                          manual_values: {
                            ...selection.manual_values,
                            [field]: {
                              state: event.target.value ? "known" : "empty",
                              data: event.target.value,
                            },
                          },
                        },
                      })
                    }
                  />
                  <small>
                    Введите фактическое значение. Пустое поле не означает
                    очистку.
                  </small>
                </label>
              ))}
            {(!selection.enrollment.create ||
              selection.enrollment.target.account.provider === "ozon") && (
              <CatalogSyncFacts
                scope={scope}
                facts={selection.facts}
                onChange={(facts) => {
                  const create = selection.enrollment.create;
                  const values = { ...create?.values };
                  delete values.category;
                  delete values.attributes;
                  onChange({
                    ...selected,
                    [id]: {
                      ...selection,
                      facts,
                      enrollment: create
                        ? {
                            ...selection.enrollment,
                            create: {
                              ...create,
                              values: {
                                ...values,
                                ...facts.values,
                              },
                            },
                          }
                        : selection.enrollment,
                    },
                  });
                }}
              />
            )}
          </fieldset>
        </article>
      ))}
    </section>
  );
}
