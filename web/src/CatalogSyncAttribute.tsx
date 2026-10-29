import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "./api";
import { catalogURL, type Attribute, type Scope } from "./CatalogSyncTypes";

type Option = { id: string; name: string };
type OptionPage = { items: Option[]; next_cursor?: string };

export function CatalogSyncAttribute({
  scope,
  categoryID,
  attribute,
  value,
  choice,
  inputID,
  onChange,
}: {
  scope: Scope;
  categoryID: string;
  attribute: Attribute;
  value: string;
  choice?: Option;
  inputID: string;
  onChange: (value: string, option?: Option) => void;
}) {
  const dictionary = attribute.dictionary || attribute.type === "select";
  const [loaded, setLoaded] = useState(false);
  const [cursor, setCursor] = useState("");
  const [history, setHistory] = useState<string[]>([]);
  const query = useQuery({
    queryKey: [
      "catalog-attribute-options",
      scope,
      categoryID,
      attribute.id,
      cursor,
    ],
    queryFn: ({ signal }) =>
      api<OptionPage>(
        catalogURL("attributes", scope, {
          provider: "ozon",
          category_id: categoryID,
          attribute_id: attribute.id,
          cursor,
          limit: "20",
        }),
        signal,
      ),
    enabled: !!dictionary && loaded,
    retry: false,
  });
  if (!dictionary && attribute.type !== "boolean")
    return (
      <input
        id={inputID}
        type={attribute.type === "number" ? "number" : "text"}
        step={attribute.type === "number" ? "any" : undefined}
        value={value}
        onChange={(event) => onChange(event.target.value)}
      />
    );
  const options = query.data?.items || attribute.options || [];
  return (
    <div className="catalog-attribute-input">
      <select
        id={inputID}
        value={value}
        onChange={(event) =>
          onChange(
            event.target.value,
            options.find((option) => option.id === event.target.value) ||
              (choice?.id === event.target.value ? choice : undefined),
          )
        }
      >
        <option value="">Укажите значение</option>
        {attribute.type === "boolean" && !dictionary ? (
          <>
            <option value="true">Да</option>
            <option value="false">Нет</option>
          </>
        ) : (
          <>
            {choice && !options.some((option) => option.id === choice.id) && (
              <option value={choice.id}>{choice.name}</option>
            )}
            {options.map((option) => (
              <option key={option.id} value={option.id}>
                {option.name}
              </option>
            ))}
          </>
        )}
      </select>
      {dictionary &&
        (!attribute.options?.length || attribute.options_incomplete) &&
        !loaded && (
          <button
            type="button"
            className="text-action"
            onClick={() => setLoaded(true)}
          >
            Загрузить значения справочника
          </button>
        )}
      {query.isFetching && <p role="status">Читаем значения справочника…</p>}
      {query.isError && (
        <p className="catalog-error" role="alert">
          Не удалось загрузить справочник.{" "}
          <button
            type="button"
            className="text-action"
            onClick={() => void query.refetch()}
          >
            Повторить чтение справочника
          </button>
        </p>
      )}
      {loaded && query.data && (
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
            Предыдущие значения
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
            Следующие значения
          </button>
          {!query.data.items?.length && (
            <small>
              Значения не найдены. Не заменяйте значение справочника
              произвольным текстом.
            </small>
          )}
        </div>
      )}
    </div>
  );
}
