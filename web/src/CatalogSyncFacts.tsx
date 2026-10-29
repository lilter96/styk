import { useId, useState, type FormEvent } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "./api";
import {
  catalogURL,
  requestError,
  type Category,
  type CategoryPage,
  type CatalogValue,
  type ProductFacts,
  type Scope,
} from "./CatalogSyncTypes";

import { CatalogSyncAttribute } from "./CatalogSyncAttribute";

export function CatalogSyncFacts({
  scope,
  facts,
  onChange,
}: {
  scope: Scope;
  facts?: ProductFacts;
  onChange: (facts: ProductFacts) => void;
}) {
  const [open, setOpen] = useState(false);
  const formID = useId();
  const [draft, setDraft] = useState("");
  const [search, setSearch] = useState("");
  const [cursor, setCursor] = useState("");
  const [history, setHistory] = useState<string[]>([]);
  const [selected, setSelected] = useState<Category | null>(null);
  const query = useQuery({
    queryKey: ["catalog-categories", scope, search, cursor],
    queryFn: async ({ signal }) => {
      const value = await api<CategoryPage>(
        catalogURL("categories", scope, {
          provider: "ozon",
          search,
          cursor,
          limit: "20",
        }),
        signal,
      );
      return { ...value, items: value.items || [] };
    },
    enabled: open,
    retry: false,
  });
  const schema = useQuery({
    queryKey: ["catalog-category-schema", scope, selected?.id],
    queryFn: ({ signal }) =>
      api<Category | CategoryPage>(
        catalogURL("categories", scope, {
          provider: "ozon",
          category_id: selected!.id,
        }),
        signal,
      ),
    enabled: open && !!selected,
    retry: false,
  });
  const loaded =
    (schema.data &&
      ("items" in schema.data
        ? schema.data.items?.find((item) => item.id === selected?.id)
        : schema.data)) ||
    selected;
  const category = loaded && { ...loaded, name: selected?.name || loaded.name };
  function find(event: FormEvent) {
    event.preventDefault();
    setSearch(draft.trim());
    setCursor("");
    setHistory([]);
  }
  function choose(category?: Category) {
    setSelected(category || null);
    onChange(makeCatalogFacts(category, {}));
  }
  return (
    <details
      className="catalog-facts"
      open={open}
      onToggle={(event) => setOpen(event.currentTarget.open)}
    >
      <summary>Категория Ozon и фактические характеристики</summary>
      {open && (
        <div className="catalog-facts-body">
          <p>
            Выберите категорию по справочнику. Обязательные характеристики
            заполняйте по данным товара; STYK не подставляет предположения. Для
            использования введённых данных выберите «Вручную» для категории и
            характеристик в настройках источников.
          </p>
          <form className="catalog-search" onSubmit={find}>
            <label>
              Найти категорию
              <input
                value={draft}
                onChange={(event) => setDraft(event.target.value)}
                type="search"
              />
            </label>
            <button className="secondary">Найти категорию</button>
          </form>
          {query.isFetching && <p role="status">Загружаем категории…</p>}
          {query.isError && (
            <p className="catalog-error" role="alert">
              {requestError(query.error)}{" "}
              <button
                type="button"
                className="text-action"
                onClick={() => void query.refetch()}
              >
                Повторить чтение категорий
              </button>
            </p>
          )}
          {query.data && (
            <>
              <label>
                Категория товара в Ozon
                <select
                  value={
                    selected &&
                    query.data.items.some((item) => item.id === selected.id)
                      ? selected.id
                      : ""
                  }
                  onChange={(event) =>
                    choose(
                      query.data!.items.find(
                        (item) => item.id === event.target.value,
                      ),
                    )
                  }
                >
                  <option value="">Выберите категорию</option>
                  {query.data.items.map((category) => (
                    <option value={category.id} key={category.id}>
                      {category.name}
                    </option>
                  ))}
                </select>
              </label>
              {!query.data.items.length && (
                <p>Категории не найдены. Измените поисковый запрос.</p>
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
                  Предыдущие категории
                </button>
                <button
                  type="button"
                  className="secondary"
                  disabled={!query.data.next_cursor || query.isFetching}
                  onClick={() => {
                    setHistory([...history, cursor]);
                    setCursor(query.data!.next_cursor!);
                  }}
                >
                  Следующие категории
                </button>
              </div>
            </>
          )}
          {selected && schema.isFetching && (
            <p role="status">Загружаем обязательные характеристики…</p>
          )}
          {selected && schema.isError && (
            <p className="catalog-error" role="alert">
              Не удалось загрузить характеристики категории.{" "}
              <button
                type="button"
                className="text-action"
                onClick={() => void schema.refetch()}
              >
                Повторить чтение характеристик
              </button>
            </p>
          )}
          {category && (
            <fieldset>
              <legend>{category.name}</legend>
              <p>Выбранная категория: {category.name}</p>
              {(category.attributes || []).map((attribute) => {
                const value = facts?.attributes[attribute.id] ?? "";
                const missing = attribute.required && !value.trim();
                return (
                  <div className="catalog-attribute" key={attribute.id}>
                    <label htmlFor={`${formID}-${attribute.id}`}>
                      {attribute.name}
                      {attribute.required ? " (обязательно)" : ""}
                      {attribute.unit ? `, ${attribute.unit}` : ""}
                    </label>
                    <CatalogSyncAttribute
                      key={`${category.id}:${attribute.id}`}
                      inputID={`${formID}-${attribute.id}`}
                      scope={scope}
                      categoryID={category.id}
                      attribute={attribute}
                      value={value}
                      choice={facts?.choices?.[attribute.id]}
                      onChange={(next, option) =>
                        onChange(
                          makeCatalogFacts(
                            category,
                            { ...facts?.attributes, [attribute.id]: next },
                            { ...facts?.choices, [attribute.id]: option },
                          ),
                        )
                      }
                    />
                    {missing && (
                      <small className="catalog-warning">
                        Укажите фактическое значение. Проверка покажет ошибки по
                        полям.
                      </small>
                    )}
                  </div>
                );
              })}
              {!schema.isFetching &&
                !schema.isError &&
                !!schema.data &&
                category.attributes_loaded !== false &&
                !(category.attributes || []).length && (
                  <p>
                    В справочнике нет обязательных характеристик для этой
                    категории.
                  </p>
                )}
              <button
                type="button"
                className="text-action"
                onClick={() => choose()}
              >
                Убрать категорию и введённые характеристики
              </button>
            </fieldset>
          )}
        </div>
      )}
    </details>
  );
}

export function makeCatalogFacts(
  category: Category | undefined,
  attributes: Record<string, string>,
  chosen: Record<string, { id: string; name: string } | undefined> = {},
): ProductFacts {
  if (!category) return { category_id: "", attributes: {} };
  const [categoryID, typeID] = category.id.split(":").map(Number);
  const values: Record<string, CatalogValue> = {};
  if (
    Number.isSafeInteger(categoryID) &&
    categoryID > 0 &&
    Number.isSafeInteger(typeID) &&
    typeID > 0
  ) {
    values.category = {
      state: "known",
      data: { description_category_id: categoryID, type_id: typeID },
    };
  }
  const factual = (category.attributes || [])
    .filter((attribute) => {
      if (
        !attributes[attribute.id]?.trim() ||
        !Number.isSafeInteger(Number(attribute.id)) ||
        Number(attribute.id) <= 0
      )
        return false;
      if (!attribute.dictionary && attribute.type !== "select") return true;
      const option =
        chosen[attribute.id] ||
        attribute.options?.find((item) => item.id === attributes[attribute.id]);
      return (
        !!option &&
        Number.isSafeInteger(Number(option.id)) &&
        Number(option.id) > 0
      );
    })
    .map((attribute) => {
      const text = attributes[attribute.id];
      const option =
        chosen[attribute.id] ||
        attribute.options?.find((item) => item.id === text);
      return {
        id: Number(attribute.id),
        complex_id: attribute.complex_id || 0,
        values: [
          {
            dictionary_value_id:
              attribute.type === "select" || attribute.dictionary
                ? Number(option?.id)
                : 0,
            value:
              attribute.type === "select" || attribute.dictionary
                ? option?.name || ""
                : text,
          },
        ],
      };
    });
  values.attributes = { state: "known", data: factual };
  const choices = Object.fromEntries(
    Object.entries(chosen).filter(([, option]) => option !== undefined),
  ) as Record<string, { id: string; name: string }>;
  return {
    category_id: category.id,
    attributes,
    values,
    ...(Object.keys(choices).length ? { choices } : {}),
  };
}
