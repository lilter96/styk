import { ApiError } from "./api";
import type { NativeDocumentPolicy } from "./OrdersNativeTypes";
import type { ExemplarSetInput } from "./OrdersExemplarTypes";

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
export type RelatedPostingGroup = {
  presence: OrderField<unknown>["presence"];
  posting_numbers: OrderField<string[]>;
};
export type OrderPosting = {
  related_postings?: RelatedPostingGroup;
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
export type OrderReverseStatusMapping = {
  state_id: string;
  action: string;
};
export type OrderReverseSuggestion = {
  scope: NativeScope;
  posting_number: string;
  commercial_order_id: string;
  customer_order_id: string;
  moysklad_state_id: string;
  proposed_action?: string;
  parameters_required: string[];
  expected_status?: string;
  posting_status: OrderField<string>;
  substatus: OrderField<string>;
  source_revision: string;
  target_revision: string;
  config_revision: number;
  blockers: { field?: string; code: string }[];
  ready: boolean;
};
export type OrderAutomation = {
  perpetual?: boolean;
  expires_at?: string;
  actions: string[];
  fields: string[];
  revoked_at?: string;
};
export type OrdersConfigData = {
  native_documents?: NativeDocumentPolicy;
  scope: NativeScope;
  revision: number;
  enabled: boolean;
  test_enabled: boolean;
  read_enabled?: boolean;
  read_configured?: boolean;
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
  reverse_status_mappings?: OrderReverseStatusMapping[];
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
export type OrderActionLine = {
  identity: OrderLine["identity"];
  quantity: string;
};
export type OrderActionParameters = {
  country_sku?: string;
  country_iso_code?: string;
  country_confirmed?: boolean;
  country_provided?: boolean;
  packages?: { lines: OrderActionLine[] }[];
  lines?: OrderActionLine[];
  reason_id?: string;
  reason_message?: string;
  tracking_number?: string;
  event_confirmed?: boolean;
  event_reference?: string;
  readiness_confirmed?: boolean;
  label_type?: "big_label" | "small_label";
  label_work_id?: string;
  label_printed_confirmed?: boolean;
  exemplar_work_id?: string;
  exemplars?: ExemplarSetInput;
  exemplars_provided?: boolean;
  tracking_provided?: boolean;
  reason_message_provided?: boolean;
  event_reference_provided?: boolean;
};
export type OrderActionProposal = {
  posting_number: string;
  action: string;
  parameters: OrderActionParameters;
};
export type OrderActionOptions = {
  scope: NativeScope;
  posting_number: string;
  source_revision: string;
  actions: {
    action: string;
    allowed: boolean;
    blockers: { field?: string; code: string }[] | string[];
    parameters_required: string[];
  }[];
  cancellation_reasons: { id: string; title: string }[];
  label_types: ("big_label" | "small_label")[];
};
export type OrderNativeWrite = {
  pii_mappings?: { source: string; attribute_id?: string }[];
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
export type OrderPositionPatch = {
  position_id: string;
  identity: OrderLine["identity"];
  quantity: OrderField<string>;
  reserve: OrderField<string>;
  price: OrderField<OrderMoney>;
};
export type OrderNativeMovement = {
  event?: { kind: string; occurred_at: string; confirmed: boolean };
  retry?: {
    previous_work_id: string;
    previous_command_fingerprint: string;
    event_digest: string;
  };
  applicable: OrderField<boolean>;
  parent_effect?: "UNPOSTED_DOCUMENT_ZERO_COUNTER";
  consumption?: {
    positions: { order_position_id: string; gross_demand: string }[];
  };
  organization_id: string;
  agent_id: string;
  store_id: string;
  currency_id: string;
  moment: string;
  demand_work_id?: string;
  lines: {
    identity: OrderLine["identity"];
    order_position_id: string;
    quantity: string;
    price: OrderMoney;
    discount: OrderField<string>;
    vat: OrderField<string>;
    vat_enabled: OrderField<boolean>;
  }[];
};
export type OrderCompositionReplacement = {
  confirm_replacement: boolean;
  preserved?: string[];
  retained: {
    source_identity: OrderLine["identity"];
    native_position_id: string;
  }[];
  added: OrderLine["identity"][];
  removed: {
    source_identity: OrderLine["identity"];
    native_position_id: string;
  }[];
};
export type OrderCommand = {
  scope: NativeScope;
  commercial_order_id?: string;
  position_replacement?: OrderCompositionReplacement;
  native_movement?: OrderNativeMovement;
  id: string;
  action: string;
  posting_number: string;
  correlation: { customer_order_id?: string };
  position_patch?: OrderPositionPatch;
  customer_order?: OrderNativeWrite;
  posting_action?: {
    capability?: OrderDeliveryCapability;
    parameters?: OrderActionParameters;
    lines?: OrderLine["identity"][];
    reason_id?: OrderField<string>;
    protected_input?: { reference: string; revision: number };
  };
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
export type OrderSplitResponsePosting = {
  posting_number: OrderField<string>;
  products: OrderField<{ sku: string; quantity: string }[]>;
};
export type OrderWork = {
  scope: NativeScope;
  native_document?: { id: string; kind: string; observed_at: string };
  id: string;
  state: string;
  dispatch_count?: number;
  command: OrderCommand;
  code?: string;
  automatic: boolean;
  receipt: {
    state: string;
    http_status?: number;
    posting_numbers?: string[];
    split_read_count?: number;
    unassembled_split?: {
      parent: OrderField<OrderSplitResponsePosting>;
      postings: OrderField<OrderSplitResponsePosting[]>;
    };
    observed_postings?: {
      posting_number: string;
      evidence: { observed_at: string; checked_at?: string };
    }[];
    external_id?: string;
    code?: string;
    artifact?: {
      available: boolean;
      download_url?: string;
      label_type?: string;
      expires_at?: string;
    };
    task_ids?: { id: string; type: string }[];
    country_evidence?: {
      sku: string;
      is_gtd_needed: OrderField<boolean>;
      requirement_resolved: boolean;
      country_value_observed: boolean;
      meaning: string;
      checked_at: string;
    };
  };
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
  cancelled_from_split_pending: "Заменено при разделении",
  not_accepted: "Не принято",
  sent_by_seller: "Отправлено продавцом",
};
export const orderActionLabel = (action: string) =>
  ({
    demand_create: "Создать отгрузку в МойСклад",
    salesreturn_create: "Создать возврат покупателя в МойСклад",
    mirror_create: "Создать заказ в МойСклад",
    mirror_update: "Обновить заказ в МойСклад",
    mirror_position_update: "Обновить позицию заказа в МойСклад",
    mirror_composition_update: "Заменить состав заказа в МойСклад",
    posting_pack: "Передать сборку в Ozon",
    posting_split: "Разделить отправление в Ozon",
    posting_split_unassembled: "Разделить без сборки",
    posting_cancel: "Отменить отправление в Ozon",
    posting_partial_cancel: "Отменить часть товаров в Ozon",
    posting_tracking: "Передать трек-номер в Ozon",
    posting_ship: "Передать статус «В доставке» в Ozon",
    posting_last_mile: "Передать статус последней мили в Ozon",
    posting_deliver: "Передать статус «Доставлено» в Ozon",
    posting_sent_by_seller: "Подтвердить отправку продавцом в Ozon",
    posting_label_create: "Создать этикетку в Ozon",
    posting_country_set: "Указать страну происхождения в Ozon",
    posting_exemplar_prepare: "Подготовить экземпляры в Ozon",
    posting_exemplar_set: "Передать сведения экземпляров в Ozon",
  })[action] || action;
const issueLabels: Record<string, string> = {
  NATIVE_RETRY_EXISTING_DOCUMENT_REQUIRES_REVIEW:
    "В МойСклад уже найден документ с идентификатором этого события. Новая запись не отправлена. Проверьте документ и исходную операцию; повторный документ автоматически не создаётся.",
  native_retry_prior_rejection_unavailable:
    "Отклонённая попытка не найдена для этого отправления или её результат изменился. Обновите историю; неизвестный результат требует сверки.",
  native_retry_latest_rejection_required:
    "Появилась более новая попытка этого события. Обновите историю и выберите последнюю отклонённую операцию.",
  native_retry_prior_effect_or_target_unverified:
    "Не подтверждено отсутствие применения предыдущих попыток либо изменился заказ, склад или товар. Повторная запись заблокирована; проверьте исходную операцию.",
  native_retry_protected_event_unavailable:
    "Исходные защищённые сведения события недоступны или срок их хранения истёк. Новый идентификатор события нельзя выдумывать; проверьте исходный документ и историю.",
  native_retry_physical_event_confirmation_required:
    "Для повторной попытки нужно заново подтвердить реальное складское событие. Его исходное время менять нельзя.",
  native_retry_event_identity_changed:
    "Повторная попытка должна относиться к прежнему событию и тому же действию. Создайте новый предпросмотр из отклонённой операции.",
  native_retry_authorization_changed:
    "Сведения предыдущей попытки изменились после проверки. Повторите предпросмотр; запись не отправлена.",
  native_retry_authorization_invalid:
    "Не подтверждено разрешение новой попытки. Выберите отклонённую операцию и создайте новый предпросмотр.",
  native_consumption_event_already_recorded:
    "Это складское событие уже связано с операцией. Повторный документ не создаётся; проверьте исходную операцию и её результат.",
  native_split_fulfillment_allocation_unverified:
    "Не подтверждено распределение уже отгруженного количества или полная история исходных заказов. Новое резервирование и отгрузка по этой цепочке требуют проверки. Возврат не сбрасывает использованный лимит.",
  native_unposted_parent_policy_required:
    "Заказ МойСклад не проведён. Для отдельной складской отгрузки нужно явно разрешить этот режим в настройках и подтвердить фактическое событие.",
  native_consumption_coverage_unproven:
    "Не удалось полностью проверить историю складских документов этого заказа. Новая отгрузка не отправлена; проверьте незавершённые операции.",
  native_consumption_changed:
    "История складских документов изменилась после предпросмотра. Создайте новый предпросмотр перед подтверждением.",
  SPLIT_ANCESTOR_SOURCE_OR_RESERVE_CHANGED:
    "После проверки изменился состав отправлений или резерв одного из исходных заказов. Действие не отправлено. Обновите данные и создайте новый предпросмотр всей связанной цепочки.",
  reservation_policy_ancestor_settlement_required:
    "Для этой части разделённого заказа ещё не подтверждён перенос резервов всех исходных отправлений. Сначала завершите сверку предыдущих операций; повторное резервирование заблокировано.",
  reservation_policy_ancestor_frontier_changed:
    "Позиции или резервы исходного заказа изменились после проверки. STYK сохраняет чужие резервы и требует новой сверки перед продолжением.",
  NATIVE_DOCUMENT_DELETED_REQUIRES_REVIEW:
    "Складской документ удалён в МойСклад. STYK не подтвердит его исполнение и не создаст замену автоматически. Проверьте документ и фактическое движение товара.",
  NATIVE_PARENT_DELETED_REQUIRES_REVIEW:
    "Связанный заказ удалён в МойСклад. Сверка складского действия остановлена; автоматическое повторное создание не выполняется.",
  native_document_deleted_requires_review:
    "Отгрузка-основание или связанный возврат удалены в МойСклад. Сначала проверьте документы и фактическое движение товара: удаление само по себе не подтверждает возврат остатка.",
  superseded_split_reserve_proof_required:
    "Исходное отправление заменено после разделения. Для снятия только его резерва нужны подтверждённый результат разделения, все дочерние отправления и свежие позиции заказа МойСклад.",
  SPLIT_REPLACEMENT_SOURCE_OR_RESERVE_CHANGED:
    "После разделения изменились исходное отправление, дочерние отправления или резерв МойСклад. Запись не отправлена; обновите данные и проверьте новый предпросмотр.",
  split_reserve_group_busy:
    "Дочерний заказ ещё ожидает сверки. STYK временно не меняет родительский заказ, чтобы не нарушить перенос резерва.",
  SPLIT_RESERVE_GROUP_BUSY:
    "Сначала нужно завершить сверку дочернего заказа после разделения. Изменение родительского заказа временно заблокировано; неизвестная запись повторно не отправляется.",
  SPLIT_PARENT_SOURCE_OR_RESERVE_CHANGED:
    "Состав родительского отправления или резерв заказа МойСклад изменился после проверки. Запись дочернего заказа не отправлена; обновите данные и создайте новый предпросмотр.",
  reservation_policy_split_transfer_proof_required:
    "Перенос родительского резерва ещё не подтверждён для этого дочернего заказа. Проверьте все позиции исходного заказа и результат его обновления.",
  split_reserve_transfer_pending:
    "Отправление разделено. Сначала нужно сверить перенос принадлежащего STYK резерва исходного заказа; повторное резервирование дочернего заказа заблокировано.",
  native_return_basis_unknown:
    "Нет полной подтверждённой отгрузки-основания и сведений о предыдущих возвратах. Обновите данные перед созданием возврата.",
  native_return_quantity_exceeded:
    "Количество возврата превышает остаток исходной отгрузки с учётом уже проведённых возвратов.",
  native_tax_or_discount_rounding_unverified:
    "Не подтверждён расчёт скидки или НДС сверх цены. STYK сохраняет наблюдаемый включённый НДС, но не угадывает налоговые значения и округление.",
  native_sum_rounding_unverified:
    "Сумма позиции требует неподтверждённого округления. Проверьте цену и количество исходного документа.",
  NATIVE_DOCUMENT_MOMENT_MINUTE_CONFIRMED:
    "Документ и связанный заказ сверены. Учётная дата подтверждена с документированной минутной точностью МойСклад; первоначальный ответ записи сохранён в истории.",
  TARGET_DOCUMENT_MINUTE_FACTS_CONFIRMED:
    "Поля заказа сверены с учётом минутной точности учётной даты МойСклад. Исходные времена Ozon не изменены.",
  NATIVE_ACKNOWLEDGEMENT_UNVERIFIED:
    "Ответ записи МойСклад не удалось подтвердить. Повторная запись не отправляется; результат требует отдельной сверки.",
  exemplar_weight_observed_value_required:
    "Ozon требует вес, но подтверждённого положительного значения нет. Новый вес STYK не угадывает и не вводит в этом редакторе.",
  exemplar_weight_merchant_edit_not_supported:
    "Редактирование веса здесь недоступно. Сохранённое значение передаётся без изменения только после свежей проверки Ozon.",
  exemplar_grouping_merchant_edit_not_supported:
    "Изменение числа коробок здесь недоступно. STYK может сохранить только подтверждённое текущее значение Ozon.",
  exemplar_multibox_fresh_evidence_required:
    "Нет свежего подтверждения числа коробок отправления. Обновите сведения Ozon перед изменением экземпляров.",
  exemplar_multibox_changed:
    "Число коробок изменилось после подготовки. Создайте новую проверку, чтобы не затереть изменение.",
  EXEMPLAR_WEIGHT_FRESH_READBACK_REQUIRED:
    "Ozon не вернул свежий вес экземпляра для сверки. Результат требует проверки; повторная отправка автоматически не выполняется.",
  EXEMPLAR_PRESERVED_WEIGHT_CHANGED:
    "Наблюдаемый вес отличается от сохранённого. Проверьте изменение в Ozon; повторная запись автоматически не выполняется.",
  composition_manual_confirmation_required:
    "Замена состава требует отдельного ручного предпросмотра и подтверждения. Автоматически она не выполняется.",
  existing_customerorder_required:
    "Сначала нужен связанный заказ МойСклад. Замена состава не создаёт новый заказ.",
  composition_ownership_unverified:
    "Не подтверждено владение всеми позициями или изменились реквизиты заказа. Чужие строки не заменяются.",
  composition_frontier_or_values_unverified:
    "Для замены нужны подтверждённые неотгруженные позиции, резерв, цена и налоговые сведения. Неподдерживаемые упаковки и неоднозначные значения блокируют изменение.",
  invalid_composition_intent:
    "Не удалось подготовить согласованную замену состава. Обновите данные и проверьте соответствия товаров.",
  explicit_delivery_action_required_for_profile:
    "Для этого профиля доставки нужно явно доступное действие в свежем ответе Ozon. Обновите отправление; отсутствие действия не считается разрешением.",
  conflicting_integration:
    "Данные отправления и метода доставки о службе перевозки противоречат друг другу. Действия Ozon остановлены до свежей согласованной проверки.",
  reservation_policy_status_unknown:
    "Неизвестен статус отправления. Получите свежие данные Ozon перед изменением резерва.",
  reservation_policy_status_not_pending:
    "Правило резерва действует только до передачи в доставку. Для этого статуса нужны отдельные складские действия.",
  reservation_policy_requires_applicable_true:
    "Для резервирования ожидающего отправления выберите проведение заказа МойСклад. Это не отправляет товар и не меняет статус Ozon.",
  reservation_policy_split_lineage_requires_review:
    "Отправление разделено. Проверьте родительский заказ и дочерние позиции, чтобы не зарезервировать один товар дважды.",
  reservation_policy_retained_split_proof_required:
    "Не подтверждены исходный заказ и принадлежащий STYK резерв после разделения. Обновите отправление и заказ МойСклад, затем создайте новый предпросмотр.",
  reservation_policy_retained_split_reduction_required:
    "Для сохранённой части отправления можно только уменьшить подтверждённые количество и резерв. Изменение товара, цены или НДС требует отдельной проверки.",
  reservation_policy_partial_cancellation_unknown:
    "Недостаточно данных о частичной отмене. STYK не угадывает оставшееся количество для резерва.",
  reservation_policy_shipment_or_cancellation_conflict:
    "Данные отмены или отгрузки противоречат правилу резерва. Проверьте отправление и складские документы.",
  reservation_policy_invalid_line:
    "Количество или идентичность позиции не подходят для резервирования. Проверьте исходные данные и соответствие товара.",
  reservation_policy_line_identity_mismatch:
    "Не совпадают исходные позиции и позиции заказа. Обновите данные и проверьте соответствия товаров.",
  reservation_policy_owned_release_required:
    "Не подтверждено, что освобождаемый резерв создан STYK. Чужие и неизвестные резервы автоматически не снимаются.",
  reservation_policy_not_authorized:
    "Для передачи резерва не выбрано поддерживаемое правило. Проверьте настройки резервов заказа.",
  application_frontier_ownership_unverified:
    "Нельзя изменить проведение заказа: не подтверждено, что все затронутые позиции и резервы принадлежат STYK. Проверьте ручные изменения в МойСклад и создайте новый предпросмотр.",
  shipment_address_component_too_long:
    "Поле адреса превышает ограничение МойСклад. Сокращение автоматически не выполняется: проверьте исходные данные или выберите передачу адреса одной строкой.",
  shipment_address_component_invalid:
    "Поле адреса не соответствует контракту МойСклад. Проверьте исходные данные; неизвестное значение не считается пустым.",

  position_ownership_unverified:
    "Нет подтверждённой связи позиции с ранее созданной строкой МойСклад. Автоматическая замена состава не выполняется.",
  position_ownership_or_replacement_unverified:
    "Состав или чужой резерв изменился. Проверьте заказ МойСклад: STYK обновляет только ранее согласованные позиции.",
  composition_split_replacement_required:
    "После разделения требуется изменить состав заказа. Откройте «Изменить состав заказа МойСклад», проверьте строки и количества STYK и подтвердите замену. Чужие строки сохраняются.",
  hybrid_printed_label_required:
    "Выберите этикетку этого отправления и подтвердите, что она распечатана и наклеена.",
  hybrid_label_evidence_unavailable:
    "Не удалось проверить готовую этикетку. Обновите историю операций.",
  hybrid_label_evidence_stale_or_mismatched:
    "Этикетка устарела или относится к другим данным отправления. Получите новую этикетку и создайте новый предпросмотр.",
  packing_requirements_not_observed:
    "Ozon не передал требования к комплектации. Запросите свежую проверку отправления.",
  packing_requirement_unknown:
    "Для этого поля комплектации Ozon не передал достаточных данных. Неизвестное значение не считается пустым.",
  packing_requirement_contract_changed:
    "Ozon вернул новое требование к комплектации. Нужна проверка поддерживаемого процесса.",
  packing_requirement_sku_not_in_posting:
    "Требование Ozon относится к товару, которого нет в составе отправления. Обновите данные.",
  packing_country_required:
    "Для указанного SKU нужно подтвердить страну происхождения в Ozon, затем обновить отправление.",
  packing_change_country_required:
    "Ozon требует уточнить страну происхождения указанного SKU. Заполните реальные данные в Ozon и обновите отправление.",
  packing_gtd_required:
    "Ozon требует таможенные сведения указанного SKU. Заполните их в Ozon и обновите отправление.",
  packing_mandatory_mark_required:
    "Ozon требует код маркировки указанного SKU. Подготовьте экземпляры, затем передайте реальные сведения и дождитесь проверки.",
  packing_jw_uin_required:
    "Ozon требует УИН ювелирного изделия. Укажите реальные данные экземпляров в Ozon и обновите отправление.",
  packing_rnpt_required:
    "Ozon требует РНПТ указанного SKU. Укажите реальные данные в Ozon и обновите отправление.",
  packing_imei_required:
    "Ozon требует IMEI указанного SKU. Укажите реальные данные экземпляров в Ozon и обновите отправление.",
  packing_weight_required:
    "Ozon требует фактический вес указанного SKU. Укажите измеренное значение в Ozon и обновите отправление.",
  action_not_available:
    "Ozon не разрешает это действие для текущего состояния отправления.",
  posting_action_not_available:
    "Ozon не разрешает это действие для текущего состояния отправления.",
  status_not_allowed:
    "Текущий статус отправления не допускает выбранное действие.",
  capability_unverified:
    "Для этого действия не подтверждены схема и условия доставки.",
  delivery_capability_unverified:
    "Для этого действия не подтверждены схема и условия доставки.",
  delivery_profile_stale:
    "Сведения о способе доставки устарели. Обновите источник.",
  fbo_out_of_scope: "Эту операцию с отправлением FBO выполняет Ozon.",
  event_confirmation_required: "Подтвердите, что событие фактически произошло.",
  event_reference_required: "Укажите основание фактического события.",
  readiness_confirmation_required:
    "Подтвердите фактическую сборку указанных упаковок.",
  cancellation_reason_required: "Выберите подтверждённую причину отмены Ozon.",
  packaging_quantity_mismatch:
    "Состав упаковок не совпадает с составом отправления.",
  tracking_required: "Укажите фактический трек-номер отправления.",
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
  reverse_mapping_missing:
    "Для состояния заказа МойСклад не настроено обратное предложение.",
  reverse_action_already_applied:
    "Ozon уже находится в целевом статусе этого действия.",
  reverse_transition_not_available:
    "Текущий статус Ozon не допускает это действие из состояния МойСклад.",
  reverse_source_status_unknown:
    "Не удалось подтвердить текущий статус отправления Ozon.",
};
export const orderIssueLabel = (code: string) =>
  issueLabels[code] ||
  "Условия передачи не подтверждены. Проверьте настройки и связи перед новой проверкой.";
export function orderOperationProblem(code: string | undefined) {
  return code &&
    [
      "NATIVE_DOCUMENT_DELETED_REQUIRES_REVIEW",
      "NATIVE_PARENT_DELETED_REQUIRES_REVIEW",
      "SPLIT_ANCESTOR_SOURCE_OR_RESERVE_CHANGED",
    ].includes(code)
    ? orderIssueLabel(code)
    : undefined;
}
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
