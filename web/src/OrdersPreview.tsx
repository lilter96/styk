import { useEffect, useState } from "react";
import { OrdersActionSummary } from "./OrdersActionSummary";
import {
  MoyOrderLink,
  orderActionLabel,
  orderDate,
  orderFieldText,
  orderIssueLabel,
  orderMoney,
  type OrdersMetadata,
  type OrdersPreviewData,
  type OrderNativeWrite,
  type OrderActionOptions,
} from "./OrdersTypes";

function ProposedOrder({
  value,
  metadata,
}: {
  value: OrderNativeWrite;
  metadata?: OrdersMetadata;
}) {
  const references = [
    ["Организация", value.organization_id, metadata?.organizations],
    ["Контрагент", value.agent_id, metadata?.agents],
    ["Склад", value.store_id, metadata?.stores],
    ["Состояние", value.state_id, metadata?.states],
    ["Канал продаж", value.sales_channel_id, metadata?.sales_channels],
  ] as const;
  return (
    <div className="orders-detail-body">
      <dl className="orders-facts">
        {references.map(([label, id, options]) => (
          <div key={label}>
            <dt>{label}</dt>
            <dd>
              {options?.find((item) => item.id === id)?.name ??
                id ??
                "Неизвестно"}
            </dd>
          </div>
        ))}
        <div>
          <dt>Валюта</dt>
          <dd>
            {metadata?.currencies.find((item) => item.id === value.currency_id)
              ?.iso_code ??
              value.currency_id ??
              "Неизвестно"}
          </dd>
        </div>
        <div>
          <dt>Название</dt>
          <dd>{orderFieldText(value.name)}</dd>
        </div>
        <div>
          <dt>Дата заказа</dt>
          <dd>{orderFieldText(value.moment, orderDate)}</dd>
        </div>
        <div>
          <dt>Проведён</dt>
          <dd>{orderFieldText(value.applicable)}</dd>
        </div>
      </dl>
      <p className="orders-note">
        Неизвестные значения не означают очистку поля. Сумма заказа
        рассчитывается МойСклад.
      </p>
      {(value.positions ?? []).map((position, index) => (
        <div className="orders-line" key={index}>
          <strong>
            Позиция{" "}
            {position.identity.offer_id ??
              position.identity.sku ??
              position.identity.provider_line_id ??
              index + 1}
          </strong>
          <span>
            Товар МойСклад: {position.mapping?.moysklad_id ?? "Неизвестно"}
          </span>
          <span>
            Количество: {position.quantity || "Неизвестно"} · Цена:{" "}
            {orderFieldText(position.price, orderMoney)} · Резерв:{" "}
            {orderFieldText(position.reserve)}
          </span>
        </div>
      ))}
      {(value.attributes ?? []).map((attribute) => (
        <p key={attribute.attribute_id}>
          {metadata?.attributes.find(
            (item) => item.id === attribute.attribute_id,
          )?.name ?? attribute.attribute_id}
          : {orderFieldText(attribute.value)}
        </p>
      ))}
      {value.owned_fields && (
        <p className="orders-note">
          Разрешённые поля: {value.owned_fields.join(", ")}
        </p>
      )}
    </div>
  );
}
export function OrdersPreview({
  preview,
  metadata,
  blocked,
  busy,
  onApprove,
  permittedActions = ["mirror_create", "mirror_update"],
  actionOptions = [],
}: {
  preview: OrdersPreviewData;
  metadata?: OrdersMetadata;
  blocked: boolean;
  busy: boolean;
  onApprove: (numbers: string[]) => void;
  permittedActions?: string[];
  actionOptions?: OrderActionOptions[];
}) {
  const [chosen, setChosen] = useState<string[]>([]);
  const [consent, setConsent] = useState(false);
  const [expired, setExpired] = useState(
    Date.parse(preview.expires_at) <= Date.now(),
  );
  useEffect(() => {
    const timer = setTimeout(
      () => setExpired(true),
      Math.max(0, Date.parse(preview.expires_at) - Date.now()),
    );
    return () => clearTimeout(timer);
  }, [preview.expires_at]);
  const ready = preview.decisions.filter(
    (decision) =>
      decision.ready &&
      !decision.validation?.length &&
      decision.commands?.length > 0 &&
      decision.commands.every(
        (command) =>
          permittedActions.includes(command.action) &&
          (command.action.startsWith("mirror_") ||
            !!command.posting_action?.parameters),
      ),
  );
  const eligible = ready.map((decision) => decision.posting.posting_number);
  const disabled = blocked || busy || expired;
  const outgoing = permittedActions.some(
    (action) => !action.startsWith("mirror_"),
  );
  function select(numbers: string[]) {
    setChosen(numbers);
    setConsent(false);
  }
  return (
    <section
      className="orders-section"
      aria-labelledby="orders-preview-heading"
    >
      <h2 id="orders-preview-heading">
        {outgoing ? "Проверка действия в Ozon" : "Проверка перед передачей"}
      </h2>
      <p>
        Готово: {ready.length}. Требуют внимания:{" "}
        {preview.decisions.length - ready.length}. Проверка действует до{" "}
        {orderDate(preview.expires_at)}.
      </p>
      {expired && (
        <p className="orders-warning" role="alert">
          Срок проверки истёк. Создайте новый предпросмотр перед подтверждением.
        </p>
      )}
      <button
        className="secondary"
        type="button"
        disabled={disabled || !eligible.length}
        onClick={() => select(eligible)}
      >
        Выбрать все готовые отправления
      </button>
      {preview.decisions.map((decision) => {
        const number = decision.posting.posting_number;
        const canSelect = eligible.includes(number);
        return (
          <article className="orders-posting" key={number}>
            <label className="orders-check">
              <input
                type="checkbox"
                aria-label={`Подтвердить отправление ${number}`}
                checked={chosen.includes(number)}
                disabled={disabled || !canSelect}
                onChange={(event) =>
                  select(
                    event.target.checked
                      ? [...chosen, number]
                      : chosen.filter((item) => item !== number),
                  )
                }
              />
              <strong>{number}</strong>
              <span
                className={`orders-badge ${canSelect ? "success" : "warning"}`}
              >
                {canSelect ? "Готово" : "Заблокировано"}
              </span>
            </label>
            {!outgoing && (
              <MoyOrderLink id={decision.correlation?.customer_order_id} />
            )}
            {(decision.validation ?? []).map((issue, index) => (
              <div className="orders-warning" key={index}>
                <p>{orderIssueLabel(issue.code)}</p>
                <details>
                  <summary>Сведения о блокировке</summary>
                  <p>
                    {issue.field && `${issue.field}: `}
                    <code>{issue.code}</code>
                  </p>
                </details>
              </div>
            ))}
            {!canSelect && !decision.validation?.length && (
              <p className="orders-warning">
                Нет подтверждённого разрешённого изменения. Отправление не будет
                передано.
              </p>
            )}
            {(decision.commands ?? []).map((command) => (
              <details key={command.id} open>
                <summary>{orderActionLabel(command.action)}</summary>
                {command.customer_order && (
                  <ProposedOrder
                    value={command.customer_order}
                    metadata={metadata}
                  />
                )}
                {!command.action.startsWith("mirror_") && (
                  <OrdersActionSummary
                    command={command}
                    reasonLabel={
                      actionOptions
                        .find((options) => options.posting_number === number)
                        ?.cancellation_reasons.find(
                          (reason) =>
                            reason.id ===
                            command.posting_action?.parameters?.reason_id,
                        )?.title
                    }
                  />
                )}
              </details>
            ))}
          </article>
        );
      })}
      <label className="orders-check">
        <input
          type="checkbox"
          checked={consent}
          disabled={disabled || !chosen.length}
          onChange={(event) => setConsent(event.target.checked)}
        />
        {outgoing
          ? "Я проверил параметры выбранного действия и подтверждаю отправку запроса в Ozon."
          : "Я проверил состав и поля выбранных отправлений и подтверждаю создание или обновление заказов в МойСклад."}
      </label>
      <div className="orders-actions">
        <button
          type="button"
          disabled={disabled || !consent || !chosen.length}
          onClick={() =>
            onApprove(chosen.filter((number) => eligible.includes(number)))
          }
        >
          {busy
            ? "Записываем подтверждение…"
            : outgoing
              ? `Подтвердить действие в Ozon (${chosen.length})`
              : `Передать в МойСклад (${chosen.length})`}
        </button>
        <p className="orders-note">
          Результат передачи подтверждается отдельной сверкой в истории
          операций.
        </p>
      </div>
    </section>
  );
}
