import {
  orderDate,
  orderFieldText,
  orderMoney,
  knownOrderField,
  type OrderCommand,
} from "./OrdersTypes";

export function OrdersNativeSummary({
  command,
  historical = false,
}: {
  command: OrderCommand;
  historical?: boolean;
}) {
  const document = command.native_movement;
  if (!document)
    return (
      <p className="orders-warning">
        Сведения складского документа не получены. Создайте новый предпросмотр.
      </p>
    );
  const posted = knownOrderField(document.applicable);
  return (
    <div className="orders-detail-body">
      <p className={posted === true ? "orders-warning" : "orders-note"}>
        {historical
          ? "Это согласованные параметры поручения. Его фактический результат указан в статусе операции; наличие параметров не подтверждает исполнение."
          : posted === true
            ? "Будет создан проведённый документ: это меняет складской учёт. Фактическое событие подтверждено оператором, а не определено по статусу Ozon."
            : posted === false
              ? "Будет создан непроведённый документ. Он не подтверждает физическую отгрузку или возврат товара."
              : "Проведение документа не определено."}
      </p>
      <p>Дата документа: {orderDate(document.moment)}.</p>
      <p>
        Склад МойСклад: {document.store_id}. Организация:{" "}
        {document.organization_id}. Контрагент учёта: {document.agent_id}.
      </p>
      {document.demand_work_id && (
        <p>
          Основание возврата: подтверждённая отгрузка из операции{" "}
          {document.demand_work_id}.
        </p>
      )}
      {(document.lines ?? []).map((line, index) => (
        <div className="orders-line" key={`${line.order_position_id}:${index}`}>
          <strong>
            {line.identity.offer_id ??
              line.identity.sku ??
              line.identity.provider_line_id}
          </strong>
          <p>
            Количество: {line.quantity}. Цена: {orderMoney(line.price)}. Скидка:{" "}
            {orderFieldText(line.discount)}%. НДС: {orderFieldText(line.vat)};
            учитывается: {orderFieldText(line.vat_enabled)}.
          </p>
        </div>
      ))}
      <p className="orders-note">
        Данные берутся из проверенных позиций связанного заказа. Платёж,
        курьерский вызов и изменение статуса Ozon этим действием не выполняются.
        Результат подтвердится отдельной сверкой.
      </p>
    </div>
  );
}
export function nativeDocumentURL(
  command: OrderCommand,
  externalID: string | undefined,
) {
  const kind =
    command.action === "demand_create"
      ? "demand"
      : command.action === "salesreturn_create"
        ? "salesreturn"
        : "";
  if (
    !kind ||
    !externalID ||
    !/^[\da-f]{8}-[\da-f]{4}-[\da-f]{4}-[\da-f]{4}-[\da-f]{12}$/i.test(
      externalID,
    )
  )
    return;
  return `https://online.moysklad.ru/app/#${kind}/edit?id=${encodeURIComponent(externalID)}`;
}
