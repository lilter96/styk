import { ApiError } from "./api";

export type OrderScope = { tenant: string; moy: string; ozon: string };
export type NativeScope = {
  tenant: string;
  moysklad_account: string;
  ozon_account: string;
};
export type OrderField<T> = {
  presence: "missing" | "null" | "empty" | "value" | "zero";
  value?: T;
};
export type OrderMoney = { amount: string; currency: string };
export type OrderDeliveryCapability = {
  id: string;
  revision: number;
  warehouse_id: string;
  delivery_method_id: string;
  scheme: string;
  carrier: OrderField<string>;
  destination: OrderField<string>;
  express: OrderField<boolean>;
  verified_dimensions: string[];
  observed_at: string;
  valid_until: string;
};
export type OrderLine = {
  identity: { provider_line_id?: string; sku?: string; offer_id?: string };
  quantity: string;
  name: OrderField<string>;
  price: OrderField<OrderMoney>;
  cancelled_quantity?: OrderField<string>;
};
export type OrderPosting = {
  scope: NativeScope;
  commercial_order_id: string;
  posting_number: string;
  parent_posting_number: OrderField<string>;
  child_posting_numbers: string[];
  order_number: OrderField<string>;
  status: OrderField<string>;
  substatus: OrderField<string>;
  scheme: OrderField<string>;
  warehouse_id: OrderField<string>;
  delivery_method_id?: OrderField<string>;
  delivery_profile?: OrderDeliveryCapability;
  available_actions?: string[] | null;
  lines: OrderLine[];
  financial: {
    order_total: OrderField<OrderMoney>;
    order_total_amount?: OrderField<string>;
    currency?: OrderField<string>;
  };
  times: { created_at: OrderField<string>; shipment_at?: OrderField<string> };
  evidence: {
    provider_updated_at: OrderField<string>;
    observed_at: string;
    checked_at?: string;
    revision: string;
  };
  drift: { path: string; code: string }[];
};
export type OrderMapping = {
  source: string;
  target: string;
  attribute_id?: string;
};
export type OrderStatusMapping = {
  status: string;
  substatus: OrderField<string>;
  state_id: string;
};
export type OrderAutomation = {
  expires_at: string;
  actions: string[];
  fields: string[];
  revoked_at?: string;
};
export type OrdersConfigData = {
  scope: NativeScope;
  revision: number;
  enabled: boolean;
  test_enabled: boolean;
  publication_available?: boolean;
  interval_seconds: number;
  source_freshness_seconds: number;
  preview_ttl_seconds: number;
  pii_enabled: boolean;
  pii_retention_seconds: number;
  organization_id: string;
  agent_id: string;
  store_id: string;
  state_id: string;
  sales_channel_id: string;
  currency_id?: string;
  field_mappings: OrderMapping[];
  status_mappings: OrderStatusMapping[];
  automation?: OrderAutomation | null;
  updated_at?: string;
};
export type OrderReference = { id: string; name: string };
export type OrdersMetadata = {
  scope: NativeScope;
  organizations: OrderReference[];
  agents: OrderReference[];
  stores: OrderReference[];
  sales_channels: OrderReference[];
  states: OrderReference[];
  attributes: (OrderReference & { type: string })[];
  currencies: { id: string; iso_code: string; default: boolean }[];
};
export type OrderPositionWrite = {
  identity: OrderLine["identity"];
  quantity: string;
  reserve: OrderField<string>;
  price: OrderField<OrderMoney>;
  mapping?: { moysklad_id: string; moysklad_kind: string };
};
export type OrderNativeWrite = {
  organization_id: string;
  agent_id: string;
  store_id: string;
  state_id: string;
  sales_channel_id: string;
  currency_id?: string;
  name: OrderField<string>;
  description: OrderField<string>;
  moment: OrderField<string>;
  applicable: OrderField<boolean>;
  positions: OrderPositionWrite[];
  owned_fields?: string[];
  attributes?: { attribute_id: string; value: OrderField<string> }[];
};
export type OrderCommand = {
  id: string;
  action: string;
  posting_number: string;
  correlation: { customer_order_id?: string };
  customer_order?: OrderNativeWrite;
};
export type OrderDecision = {
  posting: OrderPosting;
  ready: boolean;
  commands: OrderCommand[];
  correlation: { customer_order_id?: string };
  validation: { field?: string; code: string }[];
};
export type OrdersPreviewData = {
  id: string;
  scope: NativeScope;
  config_revision: number;
  expires_at: string;
  decisions: OrderDecision[];
};
export type OrderWork = {
  id: string;
  state: string;
  command: OrderCommand;
  code?: string;
  automatic: boolean;
  receipt: { state: string; external_id?: string; code?: string };
};
export type OrderOperation = {
  id: string;
  scope: NativeScope;
  state: string;
  created_at: string;
  targets: OrderWork[];
};
export type OrdersPostingPage = {
  postings: OrderPosting[];
  next_cursor?: string;
};
export type OrdersOperationPage = {
  operations: OrderOperation[];
  next_cursor?: string;
};

export const orderScopeBody = (scope: OrderScope): NativeScope => ({
  tenant: scope.tenant,
  moysklad_account: scope.moy,
  ozon_account: scope.ozon,
});
export const sameOrderScope = (
  value: NativeScope | undefined,
  scope: OrderScope,
) =>
  value?.tenant === scope.tenant &&
  value.moysklad_account === scope.moy &&
  value.ozon_account === scope.ozon;
export function ordersURL(
  path: string,
  scope: OrderScope,
  extra: Record<string, string> = {},
) {
  return `/api/orders/${path}?${new URLSearchParams({ ...orderScopeBody(scope), ...extra })}`;
}
export function knownOrderField<T>(
  field: OrderField<T> | undefined,
): T | undefined {
  return field && (field.presence === "value" || field.presence === "zero")
    ? field.value
    : undefined;
}
export function orderFieldText<T>(
  field: OrderField<T> | undefined,
  format?: (value: T) => string,
): string {
  const value = knownOrderField(field);
  if (value !== undefined)
    return format
      ? format(value)
      : typeof value === "boolean"
        ? value
          ? "Да"
          : "Нет"
        : String(value);
  if (field?.presence === "empty") return "Пустое значение";
  if (field?.presence === "null") return "Источник передал null";
  return "Неизвестно";
}
export function orderDate(value: string | undefined) {
  if (
    !value ||
    value.startsWith("0001-") ||
    !Number.isFinite(Date.parse(value))
  )
    return "Неизвестно";
  return new Intl.DateTimeFormat("ru-RU", {
    dateStyle: "short",
    timeStyle: "short",
  }).format(new Date(value));
}
export const orderMoney = (value: OrderMoney) =>
  `${value.amount} ${value.currency}`;
export const orderStateLabels: Record<string, string> = {
  READY: "Готово к подтверждению",
  QUEUED: "В очереди",
  CLAIMED: "Проверяем условия",
  DISPATCHING: "Отправляем",
  ACKNOWLEDGED: "Принято, ожидает сверки",
  POLLING: "Сверяем результат",
  RECONCILED: "Подтверждено сверкой",
  UNKNOWN: "Результат неизвестен",
  REJECTED: "Отклонено",
  BLOCKED: "Заблокировано",
};
export const postingStatusLabels: Record<string, string> = {
  awaiting_registration: "Ожидает регистрации",
  acceptance_in_progress: "Приёмка",
  awaiting_approve: "Ожидает подтверждения",
  awaiting_packaging: "Ожидает сборки",
  awaiting_deliver: "Ожидает отгрузки",
  arbitration: "Арбитраж",
  client_arbitration: "Спор покупателя",
  delivering: "Доставляется",
  driver_pickup: "У курьера",
  delivered: "Доставлено",
  cancelled: "Отменено",
  not_accepted: "Не принято",
  sent_by_seller: "Отправлено продавцом",
};
export const orderActionLabel = (action: string) =>
  ({
    mirror_create: "Создать заказ в МойСклад",
    mirror_update: "Обновить заказ в МойСклад",
  })[action] || action;
const issueLabels: Record<string, string> = {
  source_stale:
    "Данные Ozon устарели. Запросите проверку источника и создайте новый предпросмотр.",
  confirmed_warehouse_mapping_required:
    "Подтвердите связь склада Ozon со складом МойСклад в разделе складов.",
  warehouse_unknown: "Ozon не передал склад отправления.",
  confirmed_product_mapping_required:
    "Подтвердите связи товаров в разделе товаров.",
  native_currency_required: "Выберите валюту из справочника МойСклад.",
  scheme_unverified_or_out_of_scope:
    "Схема отправления не подтверждена для этой операции.",
  target_readback_unavailable:
    "Не удалось прочитать существующий заказ МойСклад.",
  target_native_references_conflict:
    "Реквизиты существующего заказа отличаются от настроек.",
  position_replacement_unverified:
    "Полный состав позиций заказа не подтверждён.",
  correlation_conflict: "Найдены противоречивые связи с заказом МойСклад.",
  source_ordering_quarantined:
    "Порядок обновлений источника не подтверждён. Нужна сверка.",
  cancelled_mirror_requires_explicit_false:
    "Для отменённого отправления явно выберите «Не проводить заказ».",
  cancelled_mirror_requires_explicit_zero_reserve:
    "Для отменённого отправления явно выберите нулевой резерв.",
  field_not_authorized: "Поле не разрешено настройками передачи.",
  native_attribute_unverified: "Дополнительное поле МойСклад не подтверждено.",
};
export const orderIssueLabel = (code: string) =>
  issueLabels[code] ||
  "Условия передачи не подтверждены. Проверьте настройки и связи перед новой проверкой.";
export function orderRequestError(error: unknown) {
  if (error instanceof ApiError) {
    if (error.status === 401) return error.message;
    if ([409, 410].includes(error.status))
      return "Данные или настройки изменились. Обновите данные и выполните новую проверку.";
    if (error.status === 403) return "Недостаточно прав для этого действия.";
    if (error.status === 400)
      return "Проверьте заполнение настроек и выбранные отправления.";
  }
  return "Не удалось получить подтверждённый ответ сервера. Обновите данные для проверки состояния.";
}
export function MoyOrderLink({ id }: { id: string | undefined }) {
  if (!id) return <span>Связь ещё не подтверждена</span>;
  return (
    <a
      href={`https://online.moysklad.ru/app/#customerorder/edit?id=${encodeURIComponent(id)}`}
      target="_blank"
      rel="noopener noreferrer"
    >
      Заказ МойСклад {id}
    </a>
  );
}
