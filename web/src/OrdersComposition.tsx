import { useState } from "react";
import type {
  OrderLine,
  OrderCompositionReplacement,
  OrderNativeWrite,
} from "./OrdersTypes";
import { orderFieldText, orderMoney } from "./OrdersTypes";

export type OrderCompositionProposal = {
  action: "mirror_composition_update";
  posting_number: string;
  confirm_replacement: true;
};

export function OrdersComposition({
  postingNumber,
  disabled,
  onPreview,
  onDraftChange,
}: {
  postingNumber: string;
  disabled: boolean;
  onPreview: (proposal: OrderCompositionProposal) => void;
  onDraftChange: () => void;
}) {
  const [confirmed, setConfirmed] = useState(false);
  return (
    <details>
      <summary>Изменить состав заказа МойСклад</summary>
      <p className="orders-note">
        STYK изменит свои подтверждённые строки по текущему отправлению Ozon.
        Чужие строки сохраняются без изменений. Отгруженные позиции и неполные
        данные требуют отдельной проверки.
      </p>
      <label className="orders-check orders-warning">
        <input
          type="checkbox"
          checked={confirmed}
          disabled={disabled}
          onChange={(event) => {
            setConfirmed(event.target.checked);
            onDraftChange();
          }}
        />
        Разрешаю подготовить замену состава этого заказа по текущему отправлению
        Ozon.
      </label>
      <button
        type="button"
        className="secondary"
        disabled={disabled || !confirmed}
        onClick={() =>
          onPreview({
            action: "mirror_composition_update",
            posting_number: postingNumber,
            confirm_replacement: true,
          })
        }
      >
        Проверить замену состава
      </button>
      <p className="orders-note">
        Предпросмотр не отправляет изменения. Замена потребует отдельного
        подтверждения.
      </p>
    </details>
  );
}

const lineLabel = (identity: OrderLine["identity"]) =>
  identity.offer_id ??
  identity.sku ??
  identity.provider_line_id ??
  "Неизвестная позиция";

export function OrdersCompositionSummary({
  value,
  positions,
}: {
  value: OrderCompositionReplacement;
  positions?: OrderNativeWrite["positions"];
}) {
  const retained = value.retained ?? [];
  const added = value.added ?? [];
  const removed = value.removed ?? [];
  const preserved = value.preserved ?? [];
  return (
    <div className="orders-detail-body">
      <p className="orders-warning">
        Будет заменён состав существующего заказа. Изменения ещё не отправлены.
      </p>
      <dl className="orders-facts">
        <div>
          <dt>Сохранить позиции</dt>
          <dd>{retained.length}</dd>
        </div>
        <div>
          <dt>Добавить позиции</dt>
          <dd>{added.length}</dd>
        </div>
        <div>
          <dt>Удалить позиции</dt>
          <dd>{removed.length}</dd>
        </div>
        <div>
          <dt>Чужие строки без изменений</dt>
          <dd>{preserved.length}</dd>
        </div>
      </dl>
      {added.map((identity, index) => (
        <p key={`add:${index}`}>Добавить: {lineLabel(identity)}</p>
      ))}
      {removed.map((binding) => (
        <p key={binding.native_position_id}>
          Удалить: {lineLabel(binding.source_identity)}
        </p>
      ))}
      {(positions ?? []).map((position, index) => (
        <div className="orders-line" key={index}>
          <strong>{lineLabel(position.identity)}</strong>
          <span>
            Количество: {position.quantity || "Неизвестно"} · Резерв:{" "}
            {orderFieldText(position.reserve)}
          </span>
          <span>Цена: {orderFieldText(position.price, orderMoney)}</span>
        </div>
      ))}
      <p className="orders-note">
        У сохранённых строк останутся прежние идентификаторы. Отсутствующие
        сведения не считаются нулём или разрешением очистить поле.
      </p>
    </div>
  );
}
