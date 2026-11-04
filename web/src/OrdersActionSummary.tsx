import { orderActionLabel, type OrderCommand } from "./OrdersTypes";

export function OrdersActionSummary({
  command,
  reasonLabel,
}: {
  command: OrderCommand;
  reasonLabel?: string;
}) {
  const parameters = command.posting_action?.parameters;
  if (!parameters)
    return (
      <p className="orders-warning">
        Параметры действия не получены. Создайте новый предпросмотр.
      </p>
    );
  return (
    <div className="orders-detail-body">
      <p>
        Будет отправлен запрос: {orderActionLabel(command.action)}. Фактический
        результат будет проверен отдельно.
      </p>
      {command.action === "posting_split_unassembled" && (
        <p className="orders-note">
          Изменится только распределение товаров между отправлениями. Это не
          сборка и не передача в доставку. Ответ API ещё не подтверждает
          синхронизацию: STYK должен повторно прочитать и проверить весь состав.
        </p>
      )}
      {(parameters.packages ?? []).map((item, index) => (
        <div className="orders-line" key={index}>
          <strong>Упаковка {index + 1}</strong>
          {item.lines.map((line, lineIndex) => (
            <p key={lineIndex}>
              Товар{" "}
              {line.identity.offer_id ??
                line.identity.sku ??
                line.identity.provider_line_id}
              : {line.quantity}
            </p>
          ))}
        </div>
      ))}
      {(parameters.lines ?? []).map((line, index) => (
        <p key={index}>
          Отменить товар{" "}
          {line.identity.offer_id ??
            line.identity.sku ??
            line.identity.provider_line_id}
          : {line.quantity}
        </p>
      ))}
      {parameters.reason_id && (
        <p>Причина отмены Ozon: {reasonLabel ?? parameters.reason_id}</p>
      )}
      {(parameters.tracking_number || parameters.tracking_provided) && (
        <p>Трек-номер введён; скрыт в журнале.</p>
      )}
      {(parameters.reason_message || parameters.reason_message_provided) && (
        <p>Комментарий к причине введён; скрыт в журнале.</p>
      )}
      {(parameters.event_reference || parameters.event_reference_provided) && (
        <p>Основание фактического события введено; скрыто в журнале.</p>
      )}
      {parameters.event_confirmed && (
        <p>Оператор подтвердил, что событие фактически произошло.</p>
      )}
      {parameters.readiness_confirmed && (
        <p>Оператор подтвердил фактическую сборку указанных упаковок.</p>
      )}
      {parameters.label_work_id && (
        <p>
          Этикетка: операция {parameters.label_work_id}.{" "}
          {parameters.label_printed_confirmed
            ? "Оператор подтвердил печать и нанесение на отправление."
            : "Печать не подтверждена."}
        </p>
      )}
      {(parameters.country_provided || parameters.country_iso_code) && (
        <p>
          Фактическая страна для SKU {parameters.country_sku} указана
          оператором; значение скрыто в журнале. Ozon не предоставляет отдельное
          чтение установленной страны.
        </p>
      )}
      {parameters.exemplar_work_id && (
        <p>
          Экземпляры: операция {parameters.exemplar_work_id}. Проверка
          фактических сведений выполняется отдельно.
        </p>
      )}
      {(parameters.exemplars_provided || parameters.exemplars) && (
        <p>
          Сведения экземпляров введены; маркировка и таможенные данные скрыты в
          журнале.
        </p>
      )}
      {parameters.label_type && (
        <p>
          Формат:{" "}
          {parameters.label_type === "big_label"
            ? "Большая этикетка"
            : "Малая этикетка"}
          .
        </p>
      )}
    </div>
  );
}
export function safeOrderArtifactURL(
  value: string | undefined,
): string | undefined {
  if (
    !value ||
    !value.startsWith("/api/orders/artifacts/") ||
    /[\\\r\n]/.test(value)
  )
    return;
  const url = new URL(value, window.location.origin);
  if (
    url.origin !== window.location.origin ||
    !/^\/api\/orders\/artifacts\/[\da-z_-]+$/i.test(url.pathname)
  )
    return;
  return url.pathname + url.search;
}
