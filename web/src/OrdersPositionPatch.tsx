import {
  knownOrderField,
  orderFieldText,
  orderMoney,
  type OrderPositionPatch,
} from "./OrdersTypes";

export function OrdersPositionPatch({ value }: { value: OrderPositionPatch }) {
  return (
    <div className="orders-detail-body">
      <strong>
        Точечное изменение позиции{" "}
        {value.identity.offer_id ??
          value.identity.sku ??
          value.identity.provider_line_id}
      </strong>
      <dl className="orders-facts">
        <div>
          <dt>Позиция МойСклад</dt>
          <dd>{value.position_id}</dd>
        </div>
        <div>
          <dt>Количество</dt>
          <dd>
            {knownOrderField(value.quantity) === undefined
              ? "Без изменения"
              : orderFieldText(value.quantity)}
          </dd>
        </div>
        <div>
          <dt>Резерв этой позиции</dt>
          <dd>
            {knownOrderField(value.reserve) === undefined
              ? "Без изменения"
              : orderFieldText(value.reserve)}
          </dd>
        </div>
        <div>
          <dt>Цена</dt>
          <dd>
            {knownOrderField(value.price) === undefined
              ? "Без изменения"
              : orderFieldText(value.price, orderMoney)}
          </dd>
        </div>
      </dl>
      <p className="orders-note">
        Изменяется только указанная позиция. Другие строки и их резервы
        сохраняются. Остальные изменения заказа обрабатываются отдельными
        проверками.
      </p>
    </div>
  );
}
