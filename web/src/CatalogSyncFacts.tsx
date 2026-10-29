import { useState, type FormEvent } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "./api";
import {
  catalogURL,
  requestError,
  type Attribute,
  type Category,
  type CategoryPage,
  type ProductFacts,
  type Scope,
} from "./CatalogSyncTypes";

function FactInput({
  attribute,
  value,
  onChange,
}: {
  attribute: Attribute;
  value: string;
  onChange: (value: string) => void;
}) {
  if (attribute.type === "boolean" || attribute.type === "select")
    return (
      <select value={value} onChange={(event) => onChange(event.target.value)}>
        <option value="">Укажите значение</option>
        {attribute.type === "boolean" ? (
          <>
            <option value="true">Да</option>
            <option value="false">Нет</option>
          </>
        ) : (
          attribute.options?.map((option) => (
            <option key={option.id} value={option.id}>
              {option.name}
            </option>
          ))
        )}
      </select>
    );
  return (
    <input
      type={attribute.type === "number" ? "number" : "text"}
      step={attribute.type === "number" ? "any" : undefined}
      value={value}
      onChange={(event) => onChange(event.target.value)}
    />
  );
}

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
  function find(event: FormEvent) {
    event.preventDefault();
    setSearch(draft.trim());
    setCursor("");
    setHistory([]);
  }
  function choose(category?: Category) {
    setSelected(category || null);
    onChange({ category_id: category?.id || "", attributes: {} });
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
                Категория
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
          {selected && (
            <fieldset>
              <legend>{selected.name}</legend>
              <p>Выбранная категория: {selected.name}</p>
              {selected.attributes.map((attribute) => {
                const value = facts?.attributes[attribute.id] ?? "";
                const missing = attribute.required && !value.trim();
                return (
                  <label key={attribute.id}>
                    {attribute.name}
                    {attribute.required ? " (обязательно)" : ""}
                    {attribute.unit ? `, ${attribute.unit}` : ""}
                    <FactInput
                      attribute={attribute}
                      value={value}
                      onChange={(next) =>
                        onChange({
                          category_id: selected.id,
                          attributes: {
                            ...facts?.attributes,
                            [attribute.id]: next,
                          },
                        })
                      }
                    />
                    {missing && (
                      <small className="catalog-warning">
                        Укажите фактическое значение. Проверка покажет ошибки по
                        полям.
                      </small>
                    )}
                  </label>
                );
              })}
              {!selected.attributes.length && (
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
