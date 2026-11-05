import type { OrderOperation, OrderWork } from "./OrdersTypes";

export type HistoryFilters = {
  search: string;
  status: string;
  attention: boolean;
};
export type HistoryRow = {
  id: string;
  operation: OrderOperation;
  work?: OrderWork;
};
export type HistoryResult = {
  label: string;
  tone: "success" | "progress" | "warning" | "danger" | "neutral";
  explanation: string;
};
export const historyResults: Record<string, HistoryResult> = {
  QUEUED: {
    label: "В обработке",
    tone: "progress",
    explanation:
      "Действие ожидает обработки. Изменения в сервисе ещё не подтверждены.",
  },
  CLAIMED: {
    label: "Проверяем условия",
    tone: "progress",
    explanation:
      "STYK проверяет, можно ли выполнить действие с текущими данными и настройками.",
  },
  DISPATCHING: {
    label: "Отправляем",
    tone: "progress",
    explanation: "Запрос передаётся в сервис. Дождитесь проверки результата.",
  },
  ACKNOWLEDGED: {
    label: "Ждём подтверждения",
    tone: "progress",
    explanation:
      "Сервис принял запрос. Это ещё не подтверждает, что изменения применились.",
  },
  POLLING: {
    label: "Проверяем результат",
    tone: "progress",
    explanation:
      "STYK проверяет результат в сервисе. Изменения пока не подтверждены.",
  },
  RECONCILED: {
    label: "Готово",
    tone: "success",
    explanation: "Результат подтверждён сверкой с сервисом.",
  },
  UNKNOWN: {
    label: "Нужна проверка",
    tone: "warning",
    explanation:
      "Пока не удалось подтвердить результат. Запрос мог выполниться. STYK проверяет результат, чтобы избежать повторной передачи.",
  },
  REJECTED: {
    label: "Отклонено",
    tone: "danger",
    explanation:
      "Действие отклонено. Проверьте причину и условия передачи перед новым предпросмотром.",
  },
  BLOCKED: {
    label: "Действие заблокировано",
    tone: "warning",
    explanation:
      "Условия передачи изменились. Проверьте настройки и связи, затем создайте новый предпросмотр.",
  },
};
const unavailable: HistoryResult = {
  label: "Нет подтверждённых сведений",
  tone: "neutral",
  explanation:
    "Достоверный результат недоступен. Обновите статусы или передайте технические сведения поддержке.",
};
export const historyResult = (state?: string): HistoryResult =>
  historyResults[state ?? ""] ?? unavailable;
export const historyNeedsAttention = (state: string) =>
  ["UNKNOWN", "REJECTED", "BLOCKED"].includes(state);

export function historyRows(
  operations: OrderOperation[],
  filters: HistoryFilters,
): HistoryRow[] {
  const rows: HistoryRow[] = [];
  const seen = new Set<string>();
  for (const operation of operations) {
    if (
      !operation.targets?.length &&
      !filters.search &&
      !filters.status &&
      !filters.attention
    )
      rows.push({ id: JSON.stringify([operation.id, null]), operation });
    for (const work of operation.targets ?? []) {
      const id = JSON.stringify([operation.id, work.id]);
      if (seen.has(id) || !historyWorkMatches(work, filters)) continue;
      seen.add(id);
      rows.push({ id, operation, work });
    }
  }
  return rows;
}
function historyWorkMatches(work: OrderWork, filters: HistoryFilters) {
  return (
    (!filters.search || work.command.posting_number === filters.search) &&
    (!filters.status || work.state === filters.status) &&
    (!filters.attention || historyNeedsAttention(work.state))
  );
}

const actionTitles: Record<string, string> = {
  mirror_create: "Создание заказа в МойСклад",
  mirror_update: "Обновление заказа в МойСклад",
  mirror_position_update: "Изменение позиции заказа",
  mirror_composition_update: "Изменение состава заказа",
  demand_create: "Отгрузка в МойСклад",
  salesreturn_create: "Возврат покупателя в МойСклад",
  posting_pack: "Передача сборки в Ozon",
  posting_split: "Разделение отправления Ozon",
  posting_split_unassembled: "Разделение без сборки",
  posting_cancel: "Отмена отправления Ozon",
  posting_partial_cancel: "Отмена части товаров Ozon",
  posting_tracking: "Передача трек-номера в Ozon",
  posting_ship: "Передача в доставку Ozon",
  posting_last_mile: "Передача на последнюю милю Ozon",
  posting_deliver: "Подтверждение доставки Ozon",
  posting_sent_by_seller: "Подтверждение отправки продавцом",
  posting_label_create: "Создание этикетки Ozon",
  posting_country_set: "Указание страны происхождения",
  posting_exemplar_prepare: "Подготовка экземпляров Ozon",
  posting_exemplar_set: "Передача сведений экземпляров",
};
const completedTitles: Record<string, string> = {
  mirror_create: "Заказ создан в МойСклад",
  mirror_update: "Заказ обновлён в МойСклад",
  mirror_position_update: "Позиция заказа изменена",
  mirror_composition_update: "Состав заказа изменён",
  demand_create: "Отгрузка создана в МойСклад",
  salesreturn_create: "Возврат создан в МойСклад",
  posting_pack: "Сборка передана в Ozon",
  posting_split: "Отправление разделено в Ozon",
  posting_split_unassembled: "Отправление разделено без сборки",
  posting_cancel: "Отправление отменено в Ozon",
  posting_partial_cancel: "Часть товаров отменена в Ozon",
  posting_tracking: "Трек-номер передан в Ozon",
  posting_ship: "Передача в доставку подтверждена",
  posting_last_mile: "Передача на последнюю милю подтверждена",
  posting_deliver: "Доставка подтверждена в Ozon",
  posting_sent_by_seller: "Отправка продавцом подтверждена",
  posting_label_create: "Этикетка создана в Ozon",
  posting_country_set: "Требование указать страну снято",
  posting_exemplar_prepare: "Сведения экземпляров подготовлены",
  posting_exemplar_set: "Сведения экземпляров переданы",
};
export const historyActionTitle = (action?: string, state?: string) =>
  (state === "RECONCILED" ? completedTitles[action ?? ""] : undefined) ??
  actionTitles[action ?? ""] ??
  "Действие с заказом";
export const historyInitiator = (work?: OrderWork) =>
  !work ? "Инициатор не указан" : work.automatic ? "Автоматически" : "Вручную";
export const historyService = (work?: OrderWork) =>
  !work
    ? "Не указан"
    : work.command.action.startsWith("posting_")
      ? "Ozon"
      : ["demand_create", "salesreturn_create"].includes(work.command.action) ||
          work.command.action.startsWith("mirror_")
        ? "МойСклад"
        : "Не указан";

function historyDate(value: string) {
  return value &&
    !value.startsWith("0001-") &&
    Number.isFinite(Date.parse(value))
    ? new Date(value)
    : undefined;
}
export function historyDay(value: string, now = new Date()) {
  const date = historyDate(value);
  if (!date) return "Дата не указана";
  const yesterday = new Date(now);
  yesterday.setDate(now.getDate() - 1);
  const prefix =
    date.toDateString() === now.toDateString()
      ? "Сегодня, "
      : date.toDateString() === yesterday.toDateString()
        ? "Вчера, "
        : "";
  return (
    prefix +
    new Intl.DateTimeFormat("ru-RU", {
      day: "numeric",
      month: "long",
      ...(date.getFullYear() !== now.getFullYear() ? { year: "numeric" } : {}),
    }).format(date)
  );
}
export function historyTime(value: string) {
  const date = historyDate(value);
  return date
    ? new Intl.DateTimeFormat("ru-RU", {
        hour: "2-digit",
        minute: "2-digit",
      }).format(date)
    : "—";
}
export function historyGroups(rows: HistoryRow[]) {
  const groups: { key: string; label: string; rows: HistoryRow[] }[] = [];
  for (const row of rows) {
    const date = historyDate(row.operation.created_at);
    const key = date?.toDateString() ?? "missing";
    let group = groups.at(-1);
    if (group?.key !== key) {
      group = { key, label: historyDay(row.operation.created_at), rows: [] };
      groups.push(group);
    }
    group!.rows.push(row);
  }
  return groups;
}
