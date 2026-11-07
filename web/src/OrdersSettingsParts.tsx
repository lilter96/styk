import { Icon, type IconName } from "./UI";
import type { OrderScope } from "./OrdersTypes";

export type SettingsSection =
  | "general"
  | "warehouses"
  | "fields"
  | "statuses"
  | "lifecycle"
  | "automation"
  | "advanced";
export const settingsSections: {
  id: SettingsSection;
  label: string;
  icon: IconName;
}[] = [
  { id: "general", label: "Общие", icon: "settings" },
  { id: "warehouses", label: "Куда передавать", icon: "products" },
  { id: "fields", label: "Данные заказа", icon: "catalog" },
  { id: "statuses", label: "Статусы и действия", icon: "refresh" },
  { id: "lifecycle", label: "Работа в МойСклад", icon: "warehouse" },
  { id: "automation", label: "Автоматизация", icon: "bolt" },
  { id: "advanced", label: "Дополнительно", icon: "diagnostics" },
];
export function SettingsHelp({
  section,
}: {
  section: SettingsSection;
  scope: OrderScope;
}) {
  const help = {
    general: {
      title: "Перед передачей",
      rows: [
        "Проверьте подключение Ozon и МойСклад",
        "Подтвердите связи товаров и складов",
        "Выберите данные и разрешение передачи",
      ],
      note: "Проверить подключения можно в разделе «Интеграции».",
    },
    warehouses: {
      title: "Без путаницы со складами",
      rows: [
        "Заказ приходит со склада Ozon",
        "STYK находит подтверждённое соответствие",
        "Заказ создаётся на нужном складе МойСклад",
      ],
      note: "Одно правило — для товаров и заказов.",
    },
    fields: {
      title: "Результат передачи",
      rows: [
        "Название и дата — из выбранных полей Ozon",
        "Позиции — по подтверждённым связям товаров",
        "Сумма — из переданных количества и цен",
      ],
      note: "Это описание результата. Существующие заказы изменяются после проверки и подтверждения или по вашему разрешению.",
    },
    statuses: {
      title: "Важно понимать",
      rows: [
        "Ozon сообщает состояние доставки",
        "Ручной статус продавца сохраняется",
      ],
      note: "Обратные подсказки требуют проверки. Для действий без открытия STYK настройте раздел «Работа в МойСклад».",
    },
    lifecycle: {
      title: "Продавец работает в МойСклад",
      rows: [
        "Сотрудник меняет состояние заказа",
        "STYK замечает разрешённый переход",
        "Проверяет данные и сверяет результат",
      ],
      note: "Подтверждайте физическую передачу и возврат только после реального события. При нехватке данных действие ожидает вмешательства.",
    },
    automation: {
      title: "Как это работает",
      rows: [
        "STYK получает заказы из Ozon",
        "Проверяет товары и склады",
        "Создаёт и обновляет заказы в МойСклад",
      ],
      note: "Это разрешение только на заказы. Действия Ozon, Отгрузки и Возвраты настраиваются отдельно в разделе «Работа в МойСклад».",
    },
    advanced: {
      title: "Отдельное подтверждение",
      rows: [
        "Создание служебных полей требует согласия",
        "Складские документы настраиваются отдельно",
      ],
      note: "После неизвестного результата сначала проверьте состояние. Не повторяйте внешнюю запись вслепую.",
    },
  }[section];
  return (
    <aside className="settings-help">
      <h2>{help.title}</h2>
      <ol>
        {help.rows.map((row, index) => (
          <li key={row}>
            <span className="settings-help-icon">
              <Icon
                name={
                  index === 0
                    ? "products"
                    : index === 1
                      ? "search"
                      : "warehouse"
                }
              />
            </span>
            <span>
              {index + 1}. {row}
            </span>
          </li>
        ))}
      </ol>
      <p
        className={
          section === "automation" || section === "fields"
            ? "settings-info"
            : "settings-help-note"
        }
      >
        {help.note}
      </p>
      {section === "general" && (
        <a href="/integrations">
          Проверить подключения <Icon name="arrow" />
        </a>
      )}
    </aside>
  );
}

export function automationFieldLabel(target: string): string {
  return (
    (
      {
        name: "Название заказа",
        moment: "Дата заказа",
        description: "Описание",
        delivery_at: "Дата доставки",
        applicable: "Проведение заказа",
        state: "Статус заказа",
        attribute: "Дополнительные поля",
        "positions.quantity": "Позиции и количество",
        "positions.price": "Цены",
        "positions.reserve": "Резервы",
        "positions.vat": "НДС позиций",
        vat_enabled: "Учёт НДС",
        vat_included: "НДС в цене",
        shipment_address: "Адрес доставки",
      } as Record<string, string>
    )[target] ??
    (target.startsWith("shipment_address_full.")
      ? "Адрес доставки"
      : "Дополнительные данные")
  );
}
