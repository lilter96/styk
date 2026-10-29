import { displayValue, type CatalogValue } from "./CatalogSyncTypes";

const objectData = (value: unknown): Record<string, unknown> | null =>
  value && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : null;
const fact = (value: unknown) =>
  displayValue(value == null ? undefined : { state: "known", data: value });

export function CatalogSyncValue({
  field,
  value,
}: {
  field: string;
  value?: CatalogValue;
}) {
  if (value?.state !== "known") return <>{displayValue(value)}</>;
  const data = objectData(value.data);
  if (field === "category" && data)
    return (
      <>
        Категория: {fact(data.description_category_id)}
        <br />
        Тип товара: {fact(data.type_id)}
      </>
    );
  if (field === "dimensions" && data)
    return (
      <dl className="catalog-value-list">
        {[
          ["height", "Высота"],
          ["width", "Ширина"],
          ["depth", "Глубина"],
          ["weight", "Вес"],
        ].map(([key, label]) => (
          <div key={key}>
            <dt>{label}</dt>
            <dd>{fact(data[key])}</dd>
          </div>
        ))}
        <div>
          <dt>Единица размера</dt>
          <dd>{unit(data.dimension_unit)}</dd>
        </div>
        <div>
          <dt>Единица веса</dt>
          <dd>{unit(data.weight_unit)}</dd>
        </div>
      </dl>
    );
  if (field === "attributes" && Array.isArray(value.data))
    return value.data.length ? (
      <ul className="catalog-value-attributes">
        {value.data.map((item: unknown, index) => {
          const attribute = objectData(item);
          if (!attribute || !Array.isArray(attribute.values))
            return (
              <li key={index}>
                Не удалось отобразить характеристику. Выполните новую проверку.
              </li>
            );
          return (
            <li key={index}>
              Характеристика {fact(attribute.id)}:{" "}
              {attribute.values
                .map((item: unknown) => {
                  const option = objectData(item);
                  return option ? fact(option.value) : "Нет данных";
                })
                .join(", ")}
            </li>
          );
        })}
      </ul>
    ) : (
      <>Характеристики не указаны</>
    );
  return <>{displayValue(value)}</>;
}

function unit(value: unknown) {
  const units: Record<string, string> = {
    mm: "Миллиметры",
    cm: "Сантиметры",
    in: "Дюймы",
    g: "Граммы",
    kg: "Килограммы",
    lb: "Фунты",
  };
  return typeof value === "string"
    ? units[value] || "Единица не распознана"
    : "Нет данных";
}
