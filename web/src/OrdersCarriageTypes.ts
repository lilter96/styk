import type { NativeScope, OrderLine } from "./OrdersTypes";
export type CarriageAction = "CREATE" | "APPROVE";
export type CarriagePlan = {
  id: string;
  scope: NativeScope;
  action: CarriageAction;
  config_revision: number;
  credentials: { ozon: number; moysklad: number };
  delivery_method_id: string;
  warehouse_id: string;
  carriage_id?: string;
  departure_date?: string;
  containers_count?: number;
  all_blr_traceable?: boolean;
  snapshot?: {
    complete?: boolean;
    observed_at?: string;
    fingerprint?: string;
    header?: {
      status: string;
      available_actions: string[] | null;
      first_mile_type?: string;
      integration_type?: string;
    };
  };
  members: {
    posting_number: string;
    status: string;
    ready: boolean;
    blockers: { code: string; field?: string }[] | null;
    lines?: OrderLine[];
  }[];
  membership_fingerprint: string;
  fingerprint: string;
  created_at: string;
  expires_at: string;
};
export type CarriageWork = {
  id: string;
  scope: NativeScope;
  plan: CarriagePlan;
  preview_id: string;
  config_revision: number;
  state: string;
  code?: string;
  carriage_id?: string;
  receipt?: {
    state?: string;
    external_id?: string;
    http_status?: number;
    code?: string;
  };
  dispatch_count: number;
  created_at: string;
  updated_at: string;
};
export type CarriagePreview = {
  id: string;
  plan: CarriagePlan;
  validation: { code: string; field?: string }[];
  existing_work?: CarriageWork;
};
export function carriageIssue(code: string) {
  if (/contract|discovery|unsupported/i.test(code))
    return "Контракт, способ доставки или профиль не подтверждён. Запись заблокирована; проверьте поддерживаемость FBS для этого склада.";
  if (/member|composition|posting|batch/i.test(code))
    return "Состав партии изменился, неполон или содержит неподтверждённые отправления. Перечитайте всю партию; прежнее согласие не применяется.";
  if (/stale|expired|revision|credential/i.test(code))
    return "Проверка или разрешение устарели. Получите новый предпросмотр текущей партии.";
  if (/authority|permission|owner|consent/i.test(code))
    return "Не подтверждены полномочия владельца или согласие на всю партию. Проверьте аккаунт и разрешения.";
  if (/unknown|ambiguous/i.test(code))
    return "Результат неизвестен. Допустимо только перечитывание; повторная запись заблокирована.";
  if (/ready|physical/i.test(code))
    return "Не подтверждена фактическая готовность всей партии. Не создавайте партию по одному статусу заказа.";
  return "Условия операции не подтверждены. Проверьте актуальный состав, профиль доставки и историю партии.";
}
export function carriageState(work: CarriageWork) {
  if (work.state === "RECONCILED")
    return work.plan.action === "CREATE"
      ? "Партия создана, результат сверён"
      : "Подтверждение партии сверено";
  return (
    (
      {
        UNKNOWN: "Результат неизвестен — только перечитывание",
        QUEUED: "Операция в очереди",
        CLAIMED: "Проверяем всю партию",
        DISPATCHING: "Запрос отправляется в Ozon",
        ACKNOWLEDGED: "Запрос принят, ждём независимой сверки",
        POLLING: "Сверяем партию",
        BLOCKED: "Нужна проверка партии",
        REJECTED: "Ozon отклонил операцию",
      } as Record<string, string>
    )[work.state] ?? "Состояние операции требует проверки"
  );
}
