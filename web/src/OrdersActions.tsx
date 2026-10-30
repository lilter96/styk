import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "./api";
import {
  orderActionLabel,
  knownOrderField,
  orderFieldText,
  orderIssueLabel,
  orderRequestError,
  ordersURL,
  sameOrderScope,
  type OrderScope,
  type OrderPosting,
  type OrderActionOptions,
  type OrderActionProposal,
  type OrderActionParameters,
} from "./OrdersTypes";

export const ozonOrderActions = [
  "posting_pack",
  "posting_split",
  "posting_cancel",
  "posting_partial_cancel",
  "posting_tracking",
  "posting_ship",
  "posting_last_mile",
  "posting_deliver",
  "posting_sent_by_seller",
  "posting_label_create",
];
const packageActions = ["posting_pack", "posting_split"];
const eventActions = [
  "posting_ship",
  "posting_last_mile",
  "posting_deliver",
  "posting_sent_by_seller",
];
const cancelActions = ["posting_cancel", "posting_partial_cancel"];
const eventLabels: Record<string, string> = {
  posting_ship:
    "Подтверждаю, что отправление rFBS фактически передано в доставку.",
  posting_last_mile:
    "Подтверждаю фактическую передачу отправления на последнюю милю доставки.",
  posting_deliver:
    "Подтверждаю, что покупатель фактически получил отправление.",
  posting_sent_by_seller:
    "Подтверждаю, что продавец фактически отправил это отправление.",
};
const lineKey = (posting: OrderPosting, index: number) =>
  JSON.stringify(posting.lines[index].identity);
const positive = (value: string) =>
  /^\d+(?:\.\d+)?$/.test(value) && /[1-9]/.test(value);
function decimalTotal(values: string[]): bigint[] {
  const scale = Math.max(
    ...values.map((value) => value.split(".")[1]?.length ?? 0),
  );
  return values.map((value) =>
    BigInt(
      value.replace(".", "") +
        "0".repeat(scale - (value.split(".")[1]?.length ?? 0)),
    ),
  );
}
export function orderAllocationValid(
  posting: OrderPosting,
  allocations: Record<string, string>[],
  complete: boolean,
): boolean {
  if (
    !posting.lines.length ||
    allocations.some((allocation) => !Object.values(allocation).some(positive))
  )
    return false;
  return posting.lines.every((line, index) => {
    const quantities = allocations.map(
      (allocation) => allocation[lineKey(posting, index)] || "0",
    );
    if (
      ![line.quantity, ...quantities].every((value) =>
        /^\d+(?:\.\d+)?$/.test(value),
      )
    )
      return false;
    const [source, ...parts] = decimalTotal([line.quantity, ...quantities]);
    const total = parts.reduce((sum, value) => sum + value, 0n);
    return complete ? total === source : total <= source;
  });
}
function allocationLines(
  posting: OrderPosting,
  allocation: Record<string, string>,
) {
  return posting.lines.flatMap((line, index) =>
    positive(allocation[lineKey(posting, index)] ?? "")
      ? [
          {
            identity: { ...line.identity },
            quantity: allocation[lineKey(posting, index)],
          },
        ]
      : [],
  );
}
export function OrdersActions({
  scope,
  posting,
  disabled,
  onPreview,
  onDraftChange,
}: {
  scope: OrderScope;
  posting: OrderPosting;
  disabled: boolean;
  onPreview: (proposal: OrderActionProposal) => void;
  onDraftChange: () => void;
}) {
  const [open, setOpen] = useState(false);
  const [action, setAction] = useState("");
  const [allocations, setAllocations] = useState<Record<string, string>[]>([
    {},
  ]);
  const [reason, setReason] = useState("");
  const [reasonMessage, setReasonMessage] = useState("");
  const [tracking, setTracking] = useState("");
  const [eventReference, setEventReference] = useState("");
  const [eventConfirmed, setEventConfirmed] = useState(false);
  const [ready, setReady] = useState(false);
  const [labelType, setLabelType] = useState("");
  const options = useQuery({
    queryKey: [
      "orders-action-options",
      scope,
      posting.posting_number,
      posting.evidence.revision,
    ],
    enabled: open,
    queryFn: async ({ signal }) => {
      const value = await api<OrderActionOptions>(
        ordersURL("action-options", scope, {
          posting_number: posting.posting_number,
        }),
        signal,
      );
      if (
        !sameOrderScope(value.scope, scope) ||
        value.posting_number !== posting.posting_number ||
        value.source_revision !== posting.evidence.revision
      )
        throw new Error("action evidence mismatch");
      return value;
    },
    retry: false,
    staleTime: 0,
    refetchOnWindowFocus: false,
  });
  const option = options.data?.actions.find((item) => item.action === action);
  const packaging = packageActions.includes(action);
  const event = eventActions.includes(action);
  const cancellation = cancelActions.includes(action);
  const wrongShipScheme =
    action === "posting_ship" && knownOrderField(posting.scheme) !== "rFBS";
  const parameters = option?.parameters_required ?? [];
  const knownParameters = [
    "packages",
    "lines",
    "reason_id",
    "reason_message",
    "tracking_number",
    "event_confirmed",
    "event_reference",
    "readiness_confirmed",
    "label_type",
  ];
  const unsupportedParameters = parameters.some(
    (parameter) => !knownParameters.includes(parameter),
  );
  const quantitiesValid =
    (!packaging && action !== "posting_partial_cancel") ||
    orderAllocationValid(posting, allocations, packaging);
  const canPreview =
    !options.isError &&
    !options.isFetching &&
    !wrongShipScheme &&
    !!option?.allowed &&
    !option.blockers?.length &&
    !unsupportedParameters &&
    quantitiesValid &&
    (!packaging ||
      (ready && (action !== "posting_split" || allocations.length >= 2))) &&
    (!cancellation ||
      !!options.data?.cancellation_reasons.some(
        (item) => item.id === reason,
      )) &&
    (!parameters.includes("reason_message") || !!reasonMessage.trim()) &&
    (action !== "posting_tracking" || !!tracking.trim()) &&
    (!event || (eventConfirmed && !!eventReference.trim())) &&
    (action !== "posting_label_create" ||
      !!options.data?.label_types.includes(
        labelType as "big_label" | "small_label",
      ));
  function changed() {
    onDraftChange();
  }
  function choose(next: string) {
    setAction(next);
    setAllocations([{}]);
    setReason("");
    setReasonMessage("");
    setTracking("");
    setEventReference("");
    setEventConfirmed(false);
    setReady(false);
    setLabelType("");
    changed();
  }
  function buildProposal() {
    if (disabled || !canPreview) return;
    const parameters: OrderActionParameters = {};
    if (packaging) {
      parameters.packages = allocations.map((allocation) => ({
        lines: allocationLines(posting, allocation),
      }));
      parameters.readiness_confirmed = ready;
    }
    if (action === "posting_partial_cancel")
      parameters.lines = allocationLines(posting, allocations[0]);
    if (cancellation) {
      parameters.reason_id = reason;
      if (reasonMessage.trim())
        parameters.reason_message = reasonMessage.trim();
    }
    if (action === "posting_tracking")
      parameters.tracking_number = tracking.trim();
    if (event) {
      parameters.event_confirmed = eventConfirmed;
      parameters.event_reference = eventReference.trim();
    }
    if (action === "posting_label_create")
      parameters.label_type = labelType as "big_label" | "small_label";
    onPreview({ posting_number: posting.posting_number, action, parameters });
  }
  return (
    <details
      className="orders-action-form"
      onToggle={(event) => setOpen(event.currentTarget.open)}
    >
      <summary>Действия с отправлением в Ozon</summary>
      <div className="orders-detail-body">
        <p>
          Выберите действие и проверьте его параметры. Отдельное подтверждение
          потребуется после предпросмотра.
        </p>
        <p className="orders-note">
          Отгрузку FBS обрабатывает Ozon. Статус «В доставке» здесь передаётся
          только для rFBS и фактически произошедшего события.
        </p>
        {options.isFetching && (
          <p role="status">Проверяем доступность действий…</p>
        )}
        {options.isError && (
          <p className="orders-error" role="alert">
            {orderRequestError(options.error)}{" "}
            <button
              className="text-action"
              type="button"
              onClick={() => void options.refetch()}
            >
              Обновить доступные действия
            </button>
          </p>
        )}
        {options.data && (
          <>
            <label>
              Действие для отправления {posting.posting_number}
              <select
                value={action}
                disabled={disabled || options.isFetching}
                onChange={(event) => choose(event.target.value)}
              >
                <option value="">Выберите действие</option>
                {options.data.actions
                  .filter((item) => ozonOrderActions.includes(item.action))
                  .map((item) => (
                    <option value={item.action} key={item.action}>
                      {orderActionLabel(item.action)}
                      {item.allowed ? "" : " — пока недоступно"}
                    </option>
                  ))}
              </select>
            </label>
            {!options.data.actions.some((item) =>
              ozonOrderActions.includes(item.action),
            ) && (
              <p className="orders-empty">
                Поддерживаемые действия для отправления пока не подтверждены.
                Обновите источник и проверьте условия доставки.
              </p>
            )}
            {option && (
              <>
                {wrongShipScheme && (
                  <p className="orders-warning">
                    Передача статуса «В доставке» здесь доступна только для
                    rFBS. Отгрузку FBS обрабатывает Ozon.
                  </p>
                )}
                {(option.blockers ?? []).map((blocker, index) => {
                  const code =
                    typeof blocker === "string" ? blocker : blocker.code;
                  return (
                    <div className="orders-warning" key={index}>
                      <p>{orderIssueLabel(code)}</p>
                      <details>
                        <summary>Сведения об ограничении</summary>
                        <code>{code}</code>
                      </details>
                    </div>
                  );
                })}
                {!option.allowed && !option.blockers?.length && (
                  <p className="orders-warning">
                    Текущие условия Ozon не разрешают это действие. Обновите
                    источник и проверьте статус, схему и способ доставки.
                  </p>
                )}
                {unsupportedParameters && (
                  <p className="orders-warning">
                    Для этого действия нужны дополнительные параметры, которые
                    пока нельзя задать в этом разделе.
                  </p>
                )}
                {(packaging || action === "posting_partial_cancel") && (
                  <fieldset>
                    <legend>
                      {packaging ? "Состав упаковок" : "Какие товары отменить"}
                    </legend>
                    <p className="orders-note">
                      Укажите количество для каждой позиции.{" "}
                      {packaging
                        ? "Весь состав отправления должен быть распределён по упаковкам."
                        : "Количество не может превышать состав отправления."}
                    </p>
                    {allocations.map((allocation, packageIndex) => (
                      <fieldset
                        className="orders-action-package"
                        key={packageIndex}
                      >
                        <legend>
                          {packaging
                            ? `Упаковка ${packageIndex + 1}`
                            : "Товары"}
                        </legend>
                        {posting.lines.map((line, lineIndex) => (
                          <label key={lineKey(posting, lineIndex)}>
                            {orderFieldText(line.name)} ·{" "}
                            {line.identity.offer_id ??
                              line.identity.sku ??
                              line.identity.provider_line_id}
                            <small>В отправлении: {line.quantity}</small>
                            <input
                              type="text"
                              inputMode="decimal"
                              maxLength={24}
                              pattern="[0-9]+([.][0-9]+)?"
                              aria-label={`Количество ${line.identity.offer_id ?? line.identity.sku ?? line.identity.provider_line_id} в ${packaging ? `упаковке ${packageIndex + 1}` : "отмене"}`}
                              value={
                                allocation[lineKey(posting, lineIndex)] ?? ""
                              }
                              disabled={disabled}
                              onChange={(event) => {
                                setAllocations((previous) =>
                                  previous.map((item, index) =>
                                    index === packageIndex
                                      ? {
                                          ...item,
                                          [lineKey(posting, lineIndex)]:
                                            event.target.value,
                                        }
                                      : item,
                                  ),
                                );
                                setReady(false);
                                changed();
                              }}
                            />
                          </label>
                        ))}
                        {packaging && allocations.length > 1 && (
                          <button
                            type="button"
                            className="text-action"
                            disabled={disabled}
                            onClick={() => {
                              setAllocations((previous) =>
                                previous.filter(
                                  (_, index) => index !== packageIndex,
                                ),
                              );
                              setReady(false);
                              changed();
                            }}
                          >
                            Убрать упаковку {packageIndex + 1}
                          </button>
                        )}
                      </fieldset>
                    ))}
                    {packaging && (
                      <button
                        className="secondary"
                        type="button"
                        disabled={disabled || allocations.length >= 20}
                        onClick={() => {
                          setAllocations((previous) => [...previous, {}]);
                          setReady(false);
                          changed();
                        }}
                      >
                        Добавить упаковку
                      </button>
                    )}
                    {!quantitiesValid && (
                      <p className="orders-warning">
                        Проверьте количества: каждая упаковка или отмена должна
                        содержать товары, а общий состав должен соответствовать
                        отправлению.
                      </p>
                    )}
                  </fieldset>
                )}
                {packaging && (
                  <label className="orders-check">
                    <input
                      type="checkbox"
                      checked={ready}
                      disabled={disabled || !quantitiesValid}
                      onChange={(event) => {
                        setReady(event.target.checked);
                        changed();
                      }}
                    />
                    Подтверждаю, что указанные товары фактически собраны и
                    распределены по этим упаковкам.
                  </label>
                )}
                {cancellation && (
                  <>
                    <label>
                      Причина отмены
                      <select
                        value={reason}
                        disabled={disabled}
                        onChange={(event) => {
                          setReason(event.target.value);
                          changed();
                        }}
                      >
                        <option value="">Выберите причину Ozon</option>
                        {options.data.cancellation_reasons.map((item) => (
                          <option key={item.id} value={item.id}>
                            {item.title}
                          </option>
                        ))}
                      </select>
                    </label>
                    {!options.data.cancellation_reasons.length && (
                      <p className="orders-warning">
                        Причины отмены Ozon не получены. Отмена недоступна до их
                        проверки.
                      </p>
                    )}
                    <label>
                      Комментарий к причине
                      {parameters.includes("reason_message")
                        ? " (обязательно)"
                        : " (необязательно)"}
                      <textarea
                        rows={3}
                        maxLength={500}
                        value={reasonMessage}
                        disabled={disabled}
                        onChange={(event) => {
                          setReasonMessage(event.target.value);
                          changed();
                        }}
                      />
                    </label>
                  </>
                )}
                {action === "posting_tracking" && (
                  <label>
                    Трек-номер
                    <input
                      autoComplete="off"
                      maxLength={128}
                      value={tracking}
                      disabled={disabled}
                      onChange={(event) => {
                        setTracking(event.target.value);
                        changed();
                      }}
                    />
                  </label>
                )}
                {event && (
                  <>
                    <label>
                      Основание фактического события
                      <input
                        autoComplete="off"
                        maxLength={256}
                        placeholder="Номер накладной или внутреннего подтверждения"
                        value={eventReference}
                        disabled={disabled}
                        onChange={(event) => {
                          setEventReference(event.target.value);
                          setEventConfirmed(false);
                          changed();
                        }}
                      />
                    </label>
                    <label className="orders-check">
                      <input
                        type="checkbox"
                        checked={eventConfirmed}
                        disabled={disabled || !eventReference.trim()}
                        onChange={(event) => {
                          setEventConfirmed(event.target.checked);
                          changed();
                        }}
                      />
                      {eventLabels[action]}
                    </label>
                  </>
                )}
                {action === "posting_label_create" && (
                  <label>
                    Формат этикетки
                    <select
                      value={labelType}
                      disabled={disabled}
                      onChange={(event) => {
                        setLabelType(event.target.value);
                        changed();
                      }}
                    >
                      <option value="">Выберите формат</option>
                      {options.data.label_types.map((type) => (
                        <option key={type} value={type}>
                          {type === "big_label"
                            ? "Большая этикетка"
                            : "Малая этикетка"}
                        </option>
                      ))}
                    </select>
                  </label>
                )}
                <button
                  type="button"
                  disabled={disabled || !canPreview}
                  onClick={buildProposal}
                >
                  Проверить действие в Ozon
                </button>
              </>
            )}
          </>
        )}
      </div>
    </details>
  );
}
