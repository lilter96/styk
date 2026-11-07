import type { FinancialLedger } from "./OrdersFinancialLedger";
import type {
  NativeScope,
  OrderField,
  OrderMoney,
  OrdersConfigData,
} from "./OrdersTypes";
import type { OrdersRealizationData } from "./OrdersRealizationReport";

export type CommissionPolicy = {
  id?: string;
  scope?: NativeScope;
  revision?: number;
  config_revision?: number;
  owner_id?: string;
  granted_at?: string;
  enabled: boolean;
  automatic: boolean;
  organization_id: string;
  agent_id: string;
  contract_id: string;
  currency_id: string;
  currency_iso: string;
  organization_account_id?: string;
  agent_account_id?: string;
  rate_value: string;
  price_basis: string;
  vat_enabled: OrderField<boolean>;
  vat_included: OrderField<boolean>;
  vat: string;
  return_treatment?: "" | "REQUIRE_ZERO" | "COMMISSION_RETURN";
  other_components: "ZERO_ONLY" | "PARTIAL_REVIEW";
};
export type CommissionContract = {
  id: string;
  name: string;
  organization_id: string;
  agent_id: string;
  currency_id: string;
  contract_type: string;
  reward_type: string;
  archived: boolean;
  organization_account_id?: string;
  agent_account_id?: string;
};
export type CommissionMetadata = {
  scope: NativeScope;
  contracts: CommissionContract[];
  truncated: boolean;
  observed_at: string;
};
export type CommissionPlan = {
  id: string;
  scope: NativeScope;
  config_revision: number;
  policy_revision: number;
  policy: CommissionPolicy;
  source: OrdersRealizationData;
  external_code: string;
  fingerprint: string;
  created_at: string;
  expires_at: string;
  lines: {
    source_row: number;
    quantity: string;
    price: OrderMoney;
    reward: OrderMoney;
    mapping: {
      id: string;
      version: number;
      moysklad_id: string;
      moysklad_kind: string;
    };
  }[];
  accounting?: FinancialLedger;
  returns?: CommissionPlan["lines"];
  return_totals?: CommissionPlan["totals"];
  totals: { currency: string; gross: string; reward: string; net: string };
};
export type CommissionWork = {
  document_id?: string;
  id: string;
  scope: NativeScope;
  plan: CommissionPlan;
  preview_id: string;
  config_revision: number;
  state: string;
  code?: string;
  receipt: {
    state?: string;
    external_id?: string;
    http_status?: number;
    code?: string;
  };
  dispatch_count: number;
  automatic: boolean;
  next_run_at?: string;
  created_at: string;
  updated_at: string;
  source_changed?: boolean;
};
export type CommissionPreview = {
  id?: string;
  plan?: CommissionPlan;
  validation: { field?: string; code: string }[];
  existing_work?: CommissionWork;
  source_changed?: boolean;
  analysis?: FinancialLedger;
  source_diff?: {
    component_changes?: {
      source_row: number;
      side: string;
      component: string;
      kind: string;
      before: OrderField<string>;
      after: OrderField<string>;
      before_digest?: string;
      after_digest?: string;
    }[];
    before_fingerprint: string;
    after_fingerprint: string;
    header_changed: boolean;
    added_rows: number[] | null;
    removed_rows: number[] | null;
    changed_rows: number[] | null;
  };
};

export const emptyCommissionPolicy = (): CommissionPolicy => ({
  enabled: false,
  automatic: false,
  organization_id: "",
  agent_id: "",
  contract_id: "",
  currency_id: "",
  currency_iso: "",
  rate_value: "",
  price_basis: "",
  vat_enabled: { presence: "missing" },
  vat_included: { presence: "missing" },
  vat: "",
  other_components: "ZERO_ONLY",
});

const rights = (p?: CommissionPolicy | null) =>
  p?.enabled
    ? JSON.stringify([
        p.automatic,
        p.organization_id,
        p.agent_id,
        p.contract_id,
        p.currency_id,
        p.currency_iso,
        p.organization_account_id ?? "",
        p.agent_account_id ?? "",
        p.rate_value,
        p.price_basis,
        p.vat_enabled,
        p.vat_included,
        p.vat,
        p.other_components,
        p.return_treatment ?? "",
      ])
    : "";
export const financeConsentRequired = (
  saved?: CommissionPolicy | null,
  next?: CommissionPolicy | null,
) => !!next?.enabled && rights(saved) !== rights(next);
const uuid = /^[\da-f]{8}(?:-[\da-f]{4}){3}-[\da-f]{12}$/i;
export function financeValidation(config: OrdersConfigData): string {
  const p = config.finance;
  if (!p?.enabled) return "";
  if (!config.read_enabled)
    return "Для финансовых отчётов включите получение данных Ozon в разделе «Общие».";
  if (
    ![p.organization_id, p.agent_id, p.contract_id, p.currency_id].every(
      (value) => uuid.test(value),
    ) ||
    !/^[A-Z]{3}$/.test(p.currency_iso)
  )
    return "Для отчёта комиссионера выберите организацию, контрагента, комиссионный договор и валюту.";
  if (
    !/^(?:0|[1-9]\d*)(?:\.\d+)?$/.test(p.rate_value) ||
    p.rate_value.length > 128 ||
    !/[1-9]/.test(p.rate_value)
  )
    return "Укажите подтверждённый положительный курс валюты отчёта.";
  if (!["SELLER_PRICE", "REALIZED_PRICE"].includes(p.price_basis))
    return "Выберите согласованное с бухгалтером основание цены отчёта.";
  if (
    ![p.vat_enabled, p.vat_included].every(
      (field) =>
        ["value", "zero"].includes(field.presence) &&
        typeof field.value === "boolean",
    ) ||
    !/^(?:\d|[1-9]\d|100)$/.test(p.vat)
  )
    return "Явно задайте учёт НДС, включение НДС в цену и целую ставку от 0 до 100%.";
  if (
    !["", "REQUIRE_ZERO", "COMMISSION_RETURN"].includes(
      p.return_treatment ?? "",
    ) ||
    !["ZERO_ONLY", "PARTIAL_REVIEW"].includes(p.other_components)
  )
    return "Выберите поддерживаемую политику финансовых возвратов и неучтённых компонентов.";
  if (!p.vat_enabled.value && p.vat !== "0")
    return "Если НДС не учитывается, выберите ставку 0.";
  if (p.vat_enabled.value && !p.vat_included.value)
    return "Для финансового черновика НДС должен быть включён в цену. Дополнительное начисление НДС поверх суммы отчёта не поддерживается.";
  return "";
}

export function commissionDocumentURL(
  work: CommissionWork,
): string | undefined {
  const id = work.document_id || work.receipt?.external_id;
  if (
    (work.state !== "RECONCILED" &&
      work.code !== "FINANCE_DOCUMENT_POSTED_EXTERNALLY") ||
    !id ||
    !uuid.test(id)
  )
    return;
  return `https://online.moysklad.ru/app/#commissionreportin/edit?id=${encodeURIComponent(id)}`;
}

export const commissionStateLabel = (work: CommissionWork) =>
  work.code === "FINANCE_DOCUMENT_POSTED_EXTERNALLY"
    ? "Документ проведён вне STYK — нужна сверка"
    : work.source_changed
      ? "Исходный отчёт изменился — нужна ручная сверка"
      : work.state === "RECONCILED" && work.plan.accounting?.complete === false
        ? "Непроведённый черновик сверён — остаток требует разбора"
        : ((
            {
              QUEUED: "Черновик ожидает создания",
              CLAIMED: "Проверяем условия черновика",
              DISPATCHING: "Создаём черновик",
              ACKNOWLEDGED: "Запрос принят, ждём сверки черновика",
              POLLING: "Проверяем созданный черновик",
              UNKNOWN: "Результат неизвестен — только проверка",
              RECONCILED: "Непроведённый черновик создан и сверён",
              REJECTED: "Создание отклонено",
              BLOCKED: "Нужна ручная проверка",
              READY: "Черновик подготовлен к проверке",
            } as Record<string, string>
          )[work.state] ?? "Состояние черновика требует проверки");
