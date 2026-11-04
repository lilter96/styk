import type { OrderMapping } from "./OrdersTypes";

const reserveChoices = [
  ["", "Не управлять резервом"],
  ["constant.zero", "Нулевой резерв"],
  ["posting.open_quantity", "Количество ожидающего товара"],
] as const;
const postingChoices = [
  ["", "Не изменять проведение"],
  ["constant.false", "Не проводить заказ"],
  ["posting.cancelled", "Проводить до отмены"],
  ["constant.true", "Проводить заказ"],
] as const;
const selectors = [
  ["positions.reserve", "Резерв в МойСклад", reserveChoices],
  ["applicable", "Проведение заказа МойСклад", postingChoices],
] as const;

export function OrdersReservationMapping({
  mappings,
  disabled,
  onChange,
}: {
  mappings: OrderMapping[];
  disabled: boolean;
  onChange: (mappings: OrderMapping[]) => void;
}) {
  const source = (target: string) =>
    mappings.find((item) => item.target === target)?.source ?? "";
  const change = (target: string, next: string) =>
    onChange([
      ...mappings.filter((item) => item.target !== target),
      ...(next ? [{ source: next, target }] : []),
    ]);
  return (
    <fieldset disabled={disabled}>
      <legend>Резервы и проведение</legend>
      <p className="orders-note">
        Проведение заказа — учётное действие МойСклад, а не отправка товара или
        подтверждение доставки в Ozon. STYK изменяет только свои подтверждённые
        позиции и резервы; ручные изменения требуют проверки.
      </p>
      {selectors.map(([target, label, choices]) => {
        const current = source(target);
        return (
          <label key={target}>
            {label}
            <select
              value={current}
              onChange={(event) => change(target, event.target.value)}
            >
              {!choices.some(([value]) => value === current) && (
                <option value={current}>Сохранённое правило</option>
              )}
              {choices.map(([value, title]) => (
                <option key={value} value={value}>
                  {title}
                </option>
              ))}
            </select>
          </label>
        );
      })}
      {source("positions.reserve") === "posting.open_quantity" && (
        <p className="orders-warning">
          Резерв будет равен количеству только для подтверждённых ожидающих
          отправлений с проведённым заказом. Неизвестные статусы, разделение и
          неоднозначная частичная отмена требуют проверки. Отмена освобождает
          только ранее подтверждённый резерв STYK; отгрузка не создаётся
          автоматически.
        </p>
      )}
    </fieldset>
  );
}
