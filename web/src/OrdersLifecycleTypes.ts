import type { NativeScope, OrdersConfigData } from "./OrdersTypes";

export type MerchantCommandMapping = {
  state_id: string;
  from_state_ids: string[];
  action: string;
  scheme: "FBS" | "rFBS";
  event_kind: string;
  whole_posting: boolean;
};

export type OrdersLifecyclePolicy = {
  id?: string;
  scope?: NativeScope;
  revision?: number;
  config_revision?: number;
  owner_id?: string;
  granted_at?: string;
  enabled: boolean;
  mappings: MerchantCommandMapping[];
  demand_moment: "ON_HANDOFF_CONFIRMED" | "ON_OZON_ACCEPTANCE_CONFIRMED";
  auto_demand: boolean;
  auto_return: boolean;
};

export const lifecycleActions = [
  {
    action: "posting_pack",
    label: "Передать сборку в Ozon",
    event: "assembly_ready",
    schemes: ["FBS", "rFBS"],
  },
  {
    action: "posting_ship",
    label: "Сообщить о передаче в доставку",
    event: "handoff_confirmed",
    schemes: ["rFBS"],
  },
  {
    action: "posting_last_mile",
    label: "Сообщить о последней миле",
    event: "last_mile_confirmed",
    schemes: ["rFBS"],
  },
  {
    action: "posting_deliver",
    label: "Сообщить о доставке покупателю",
    event: "delivery_confirmed",
    schemes: ["rFBS"],
  },
  {
    action: "demand_create",
    label: "Создать проведённую Отгрузку",
    event: "handoff_confirmed",
    schemes: ["FBS", "rFBS"],
  },
  {
    action: "salesreturn_create",
    label: "Создать проведённый Возврат покупателя",
    event: "return_received",
    schemes: ["FBS", "rFBS"],
  },
] as const;

export const emptyLifecycle = (): OrdersLifecyclePolicy => ({
  enabled: false,
  mappings: [],
  demand_moment: "ON_HANDOFF_CONFIRMED",
  auto_demand: false,
  auto_return: false,
});

const lifecycleRights = (policy?: OrdersLifecyclePolicy | null) =>
  policy?.enabled
    ? JSON.stringify([
        policy.mappings,
        policy.demand_moment,
        policy.auto_demand,
        policy.auto_return,
      ])
    : "";

export const lifecycleConsentRequired = (
  saved?: OrdersLifecyclePolicy | null,
  next?: OrdersLifecyclePolicy | null,
) => !!next?.enabled && lifecycleRights(saved) !== lifecycleRights(next);

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export function lifecycleValidation(config: OrdersConfigData): string {
  const policy = config.lifecycle;
  if (!policy?.enabled) return "";
  if (!config.enabled || config.read_enabled !== true)
    return "Включите получение отправлений и передачу заказов в разделе «Общие».";
  if (policy.auto_demand && policy.demand_moment !== "ON_HANDOFF_CONFIRMED")
    return "Автоматическая Отгрузка по приёмке Ozon недоступна: подтверждённое событие приёмки не получено.";
  if (!policy.mappings.length || policy.mappings.length > 30)
    return "Добавьте от 1 до 30 переходов МойСклад.";
  const keys = new Set<string>();
  const events = new Map<string, string>();
  for (const mapping of policy.mappings) {
    const action = lifecycleActions.find(
      (item) => item.action === mapping.action,
    );
    const stateKey = `${mapping.scheme}:${mapping.state_id}`;
    const key = `${stateKey}:${mapping.action}`;
    if (
      !uuid.test(mapping.state_id) ||
      !mapping.from_state_ids.length ||
      mapping.from_state_ids.length > 20 ||
      mapping.from_state_ids.some(
        (id) => !uuid.test(id) || id === mapping.state_id,
      ) ||
      new Set(mapping.from_state_ids).size !== mapping.from_state_ids.length
    )
      return "Для каждого перехода выберите новое и допустимые предыдущие состояния МойСклад.";
    if (
      !action ||
      !(action.schemes as readonly string[]).includes(mapping.scheme) ||
      action.event !== mapping.event_kind ||
      !mapping.whole_posting
    )
      return "Проверьте действие, схему и подтверждение всех позиций отправления.";
    if (keys.has(key))
      return "Одно действие не должно повторяться для одного состояния и схемы.";
    if (events.has(stateKey) && events.get(stateKey) !== mapping.event_kind)
      return "Один переход МойСклад не может подтверждать разные физические события.";
    keys.add(key);
    events.set(stateKey, mapping.event_kind);
    if (
      mapping.state_id === config.state_id ||
      config.status_mappings.some((item) => item.state_id === mapping.state_id)
    )
      return "Состояние команды продавца не должно использоваться для нового заказа или статуса, который записывает Ozon.";
    if (
      (mapping.action === "demand_create" && !policy.auto_demand) ||
      (mapping.action === "salesreturn_create" && !policy.auto_return)
    )
      return "Для складского действия включите отдельное разрешение Отгрузки или Возврата.";
  }
  if (
    (policy.auto_demand &&
      !policy.mappings.some((item) => item.action === "demand_create")) ||
    (policy.auto_return &&
      !policy.mappings.some((item) => item.action === "salesreturn_create"))
  )
    return "Добавьте переход для каждого разрешённого складского документа.";
  if (policy.auto_demand || policy.auto_return) {
    const native = config.native_documents;
    if (
      !native?.enabled ||
      (policy.auto_demand && !native.actions?.includes("demand_create")) ||
      (policy.auto_return && !native.actions?.includes("salesreturn_create"))
    )
      return "Разрешите нужные складские документы и задайте лимиты в разделе «Дополнительно».";
  }
  if (hasLifecycleCycle(policy.mappings))
    return "Переходы образуют цикл. Выберите последовательные состояния без возврата в исходное состояние.";
  return "";
}

function hasLifecycleCycle(mappings: MerchantCommandMapping[]): boolean {
  const edges = new Map<string, string[]>();
  for (const mapping of mappings) {
    for (const from of mapping.from_state_ids) {
      const key = `${mapping.scheme}:${from}`;
      edges.set(key, [
        ...(edges.get(key) ?? []),
        `${mapping.scheme}:${mapping.state_id}`,
      ]);
    }
  }
  const visited = new Map<string, number>();
  const visit = (node: string): boolean => {
    if (visited.get(node) === 1) return true;
    if (visited.get(node) === 2) return false;
    visited.set(node, 1);
    if ((edges.get(node) ?? []).some(visit)) return true;
    visited.set(node, 2);
    return false;
  };
  return [...edges.keys()].some(visit);
}
