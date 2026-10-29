import type { Scope } from "./Confirmation";
import { ApiError, params } from "./api";

export type { Scope };
export type Provider = "moysklad" | "ozon";
export type Authority =
  "MOYSKLAD_AUTHORITATIVE" | "OZON_AUTHORITATIVE" | "MANUAL" | "IGNORE";
export type CatalogValue = {
  state: "known" | "absent" | "empty" | "clear" | "unsupported" | "unknown";
  data?: unknown;
};
export type Identity = {
  account: { provider: Provider; id: string };
  id: string;
  offer_id?: string;
  sku?: string;
  entity_type?: string;
};
export type CatalogProduct = {
  identity: Identity;
  fields: Record<string, CatalogValue>;
  revision: string;
  variant?: boolean;
  parent?: Identity;
  lifecycle?: "active" | "archived" | "unknown";
};
export type FieldCapability = {
  field: string;
  writable: boolean;
  reason?: string;
};
export type Enrollment = {
  id: string;
  mapping_id?: string;
  mapping_version?: number;
  source: Identity;
  target: Identity;
  create?: {
    id?: string;
    external_code?: string;
    values: Record<string, CatalogValue>;
    extensions?: Record<string, CatalogValue>;
    parent?: Identity;
  };
  automatic: boolean;
  automatic_fields?: string[];
};
export type CatalogConfig = {
  scope: { tenant: string; moysklad_account: string; ozon_account: string };
  revision: number;
  enabled: boolean;
  test_enabled: boolean;
  interval_seconds: number;
  rules: Record<string, Authority>;
  targets: Enrollment[];
  updated_at: string;
  fields?: FieldCapability[];
};
// enrollments is HTTP view metadata joining canonical products to confirmed mappings.
export type ProductPage = {
  products: CatalogProduct[];
  enrollments?: Enrollment[];
  next_cursor?: string;
  total?: number;
};
export type Attribute = {
  id: string;
  name: string;
  required: boolean;
  type: "string" | "number" | "boolean" | "select";
  options?: { id: string; name: string }[];
  unit?: string;
};
export type Category = { id: string; name: string; attributes: Attribute[] };
export type CategoryPage = { items: Category[]; next_cursor?: string };
export type ProductFacts = {
  category_id: string;
  attributes: Record<string, string>;
};
export type FieldIssue = { field?: string; code: string; message?: string };
export type Change = {
  field: string;
  source: Identity;
  target: Identity;
  before: CatalogValue;
  after: CatalogValue;
  source_fingerprint?: string;
  target_fingerprint?: string;
};
export type Plan = {
  mapping_id: string;
  mapping_version: number;
  fingerprint: string;
  changes: Change[];
  conflicts: {
    field: string;
    reason: string;
    source_value: CatalogValue;
    target_value: CatalogValue;
  }[];
  skipped: { field: string; reason: string }[];
  noop: boolean;
};
export type Decision = {
  enrollment_id: string;
  source: CatalogProduct;
  target: CatalogProduct;
  target_absent: boolean;
  plan: Plan;
  commands: {
    action: string;
    target: Identity;
    values: Record<string, CatalogValue>;
  }[];
  validation: FieldIssue[];
  ready: boolean;
};
export type CatalogPreview = {
  id: string;
  scope: CatalogConfig["scope"];
  config_revision: number;
  expires_at: string;
  decisions: Decision[];
};
export type CatalogWork = {
  id: string;
  enrollment_id: string;
  state: string;
  command: {
    target: Identity;
    action: string;
    values: Record<string, CatalogValue>;
  };
  decision: Decision;
  receipt: {
    state: string;
    task_id?: string;
    errors?: FieldIssue[];
    products?: {
      id?: string;
      offer_id?: string;
      status: string;
      errors?: FieldIssue[];
    }[];
  };
  code?: string;
};
export type CatalogOperation = {
  id: string;
  scope: CatalogConfig["scope"];
  preview_id: string;
  state: string;
  targets: CatalogWork[];
  created_at: string;
};
export type OperationPage = {
  operations: CatalogOperation[];
  next_cursor?: string;
};

export const fieldLabels: Record<string, string> = {
  name: "Название",
  description: "Описание",
  article: "Артикул",
  price: "Цена",
  currency: "Валюта",
  barcode: "Штрихкод",
  barcodes: "Штрихкоды",
  category: "Категория",
  attributes: "Характеристики",
  unit: "Единица измерения",
  images: "Изображения",
  dimensions: "Габариты",
  parent: "Родительский товар",
  lifecycle: "Архивирование",
};
export const fieldLabel = (field?: string) =>
  field ? fieldLabels[field] || "Другое поле" : "Товар";
export const providerLabel = (provider: string) =>
  provider === "moysklad"
    ? "МойСклад"
    : provider === "ozon"
      ? "Ozon"
      : "Сервис";
export const scopeBody = (scope: Scope) => ({
  tenant: scope.tenant,
  moysklad_account: scope.moy,
  ozon_account: scope.ozon,
});
export const catalogURL = (
  path: string,
  scope: Scope,
  extra: Record<string, string> = {},
) => `/api/catalog-sync/${path}?${params({ ...scopeBody(scope), ...extra })}`;
export const productName = (product: CatalogProduct) => {
  const name = product.fields?.name;
  return name?.state === "known" && typeof name.data === "string" && name.data
    ? name.data
    : product.identity.offer_id || product.identity.id || "Товар без названия";
};

export function normalizeConfig(config: CatalogConfig): CatalogConfig {
  return {
    ...config,
    rules: config.rules || {},
    targets: config.targets || [],
  };
}

export function normalizePreview(preview: CatalogPreview): CatalogPreview {
  return {
    ...preview,
    decisions: (preview.decisions || []).map((decision) => ({
      ...decision,
      commands: decision.commands || [],
      validation: decision.validation || [],
      plan: {
        ...decision.plan,
        changes: decision.plan.changes || [],
        conflicts: decision.plan.conflicts || [],
        skipped: decision.plan.skipped || [],
      },
    })),
  };
}

// Copy only canonical Config fields; HTTP capability metadata is never posted back.
export function configBody(config: CatalogConfig) {
  return {
    scope: config.scope,
    revision: config.revision,
    enabled: config.enabled,
    test_enabled: config.test_enabled,
    interval_seconds: config.interval_seconds,
    rules: config.rules,
    targets: config.targets,
    updated_at: config.updated_at,
  };
}
export function displayValue(value?: CatalogValue): string {
  if (!value || value.state === "absent") return "Нет данных";
  if (value.state === "unknown") return "Неизвестно";
  if (value.state === "unsupported") return "Не поддерживается";
  if (value.state === "empty") return "Пустое значение";
  if (value.state === "clear") return "Очистить значение";
  if (value.data === null || value.data === undefined) return "Нет данных";
  if (typeof value.data === "boolean") return value.data ? "Да" : "Нет";
  if (typeof value.data === "string" || typeof value.data === "number")
    return String(value.data);
  if (
    Array.isArray(value.data) &&
    value.data.every((item) => ["string", "number"].includes(typeof item))
  )
    return value.data.join(", ");
  return "Составное значение — проверьте характеристики";
}

const issueLabels: Record<string, string> = {
  both_sides_changed: "Оба сервиса изменили поле. Сначала разрешите конфликт.",
  unsupported_write_field: "Передача этого поля пока не поддерживается.",
  ignored: "Поле исключено из синхронизации.",
  archived_product: "Товар в архиве. Изменения пропущены.",
  manual_value_required: "Введите фактическое значение вручную.",
  source_absent: "Нет значения в источнике.",
  source_unknown: "Значение в источнике неизвестно.",
  source_unsupported: "Источник не предоставляет это поле.",
  target_absent: "Нет данных о текущем значении получателя.",
  target_unknown: "Текущее значение получателя неизвестно.",
  target_unsupported: "Получатель не поддерживает поле.",
  explicit_clear_required: "Пустое значение не очищает поле автоматически.",
  required: "Заполните обязательное поле.",
  invalid_value: "Проверьте значение поля.",
};
export const issueLabel = (code: string) =>
  issueLabels[code] ||
  "Сервис отклонил поле. Проверьте данные перед новой проверкой.";
export function requestError(error: unknown): string {
  if (!(error instanceof ApiError))
    return "Нет ответа от STYK. Проверьте соединение.";
  if (error.status === 401) return "Сессия завершилась. Войдите снова.";
  if (error.status === 409 || error.status === 410)
    return "Данные или настройки изменились. Обновите данные и выполните проверку заново.";
  if (error.status === 403) return "Нет доступа к выбранной паре аккаунтов.";
  if (error.status === 429)
    return "Слишком много запросов. Подождите и повторите чтение.";
  return "STYK не смог обработать запрос. Проверьте доступность сервиса.";
}
