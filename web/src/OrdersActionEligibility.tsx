import {
  knownOrderField,
  orderDate,
  orderFieldText,
  type OrderPosting,
} from "./OrdersTypes";

const dimensionLabels: Record<string, string> = {
  scheme: "Схема работы",
  warehouse: "Склад Ozon",
  delivery_method: "Способ доставки",
  express: "Экспресс-доставка",
  carrier: "Перевозчик",
  destination: "Тип получения",
};
export function OrdersActionEligibility({
  posting,
  freshnessSeconds,
}: {
  posting: OrderPosting;
  freshnessSeconds: number;
}) {
  const profile = posting.delivery_profile;
  const checked = posting.evidence.checked_at;
  const fresh =
    !!checked &&
    Number.isFinite(Date.parse(checked)) &&
    Date.parse(checked) <= Date.now() &&
    Date.parse(checked) + freshnessSeconds * 1000 > Date.now();
  const validProfile =
    !!profile &&
    !!profile.id &&
    profile.revision > 0 &&
    !!profile.delivery_method_id &&
    Number.isFinite(Date.parse(profile.observed_at)) &&
    Date.parse(profile.observed_at) <= Date.now() &&
    Number.isFinite(Date.parse(profile.valid_until)) &&
    Date.parse(profile.valid_until) > Date.now();
  const fbo = knownOrderField(posting.scheme)?.toUpperCase() === "FBO";
  return (
    <div className="orders-detail-body orders-action-eligibility">
      <p>
        Доступность каждого действия проверяется отдельно по текущим данным
        Ozon. Статус отправления сам по себе не разрешает действие.
      </p>
      <dl className="orders-facts">
        <div>
          <dt>Свежесть данных</dt>
          <dd>
            {fresh ? "Данные свежие" : "Нужна новая проверка источника"}
            <small>Проверено {orderDate(checked)}</small>
          </dd>
        </div>
        <div>
          <dt>Способ доставки Ozon</dt>
          <dd>{orderFieldText(posting.delivery_method_id)}</dd>
        </div>
        <div>
          <dt>Сведения об условиях доставки</dt>
          <dd>
            {validProfile
              ? "Проверены до " + orderDate(profile.valid_until)
              : "Не подтверждены"}
          </dd>
        </div>
        {profile && (
          <>
            <div>
              <dt>Перевозчик</dt>
              <dd>{orderFieldText(profile.carrier)}</dd>
            </div>
            <div>
              <dt>Тип получения</dt>
              <dd>{orderFieldText(profile.destination)}</dd>
            </div>
            <div>
              <dt>Экспресс-доставка</dt>
              <dd>{orderFieldText(profile.express)}</dd>
            </div>
          </>
        )}
      </dl>
      {fbo && (
        <p className="orders-warning">
          Доставку FBO выполняет Ozon. Операции продавца с отгрузкой и доставкой
          требуют отдельного подтверждения доступности.
        </p>
      )}
      {!fresh && (
        <p className="orders-warning">
          Запросите проверку источника перед предпросмотром действия. Старые
          данные не подтверждают текущие условия.
        </p>
      )}
      {!validProfile && (
        <p className="orders-warning">
          Способ, перевозчик или условия получения не подтверждены для операции.
          Предпросмотр покажет, какие сведения нужны.
        </p>
      )}
      {profile && (
        <p className="orders-note">
          Подтверждённые условия:{" "}
          {(profile.verified_dimensions ?? [])
            .map(
              (dimension) =>
                dimensionLabels[dimension] ?? "Дополнительное условие доставки",
            )
            .join(", ") || "Неизвестно"}
          .
        </p>
      )}
      {posting.available_actions != null &&
        posting.available_actions.length === 0 && (
          <p className="orders-warning">
            Ozon не указал доступных действий для этого отправления.
          </p>
        )}
      {posting.available_actions == null && (
        <p className="orders-note">
          Список доступных действий не получен от Ozon. Он не считается
          разрешением на отправку.
        </p>
      )}
    </div>
  );
}
