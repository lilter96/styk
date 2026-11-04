import type { OrderMapping } from "./OrdersTypes";

const fields = [
  ["city", "Город"],
  ["street", "Улица"],
  ["house", "Дом"],
  ["apartment", "Квартира"],
  ["postal_code", "Почтовый индекс"],
] as const;
export function OrdersAddressMapping({
  value,
  disabled,
  onChange,
}: {
  value: OrderMapping[];
  disabled: boolean;
  onChange: (mappings: OrderMapping[]) => void;
}) {
  function select(field: string, checked: boolean) {
    const source = field ? `customer.address.${field}` : "customer.address";
    const rest = value.filter(
      (item) =>
        item.source !== source &&
        (!checked ||
          (field
            ? item.source !== "customer.address"
            : !item.target.startsWith("shipment_address_full."))),
    );
    onChange(
      checked
        ? [
            ...rest,
            {
              source,
              target: field
                ? `shipment_address_full.${field}`
                : "shipment_address",
            },
          ]
        : rest,
    );
  }
  return (
    <fieldset disabled={disabled}>
      <legend>Адрес получателя</legend>
      <p className="orders-note">
        Адрес получателя не меняет контрагента учёта. Выберите передачу одной
        строкой или отдельных полей. Не полученные и null-значения сохраняют
        прежние сведения; явная пустая строка выбранного поля может очистить
        его.
      </p>
      <label className="orders-check">
        <input
          type="checkbox"
          checked={value.some(
            (item) =>
              item.source === "customer.address" &&
              item.target === "shipment_address",
          )}
          onChange={(event) => select("", event.target.checked)}
        />
        Адрес доставки → нативное поле «Адрес отгрузки»
      </label>
      <p className="orders-note">Отдельные нативные поля адреса МойСклад:</p>
      {fields.map(([field, title]) => (
        <label className="orders-check" key={field}>
          <input
            type="checkbox"
            checked={value.some(
              (item) =>
                item.source === `customer.address.${field}` &&
                item.target === `shipment_address_full.${field}`,
            )}
            onChange={(event) => select(field, event.target.checked)}
          />
          {title} → поле адреса отгрузки
        </label>
      ))}
      <p className="orders-note">
        Страна, регион и невыбранные поля существующего структурированного
        адреса сохраняются. Страна по её названию автоматически не
        подставляется. Если в МойСклад адрес заполнен только одной строкой,
        преобразование без проверки блокируется. Индекс в этом API ограничен
        шестью символами.
      </p>
    </fieldset>
  );
}
