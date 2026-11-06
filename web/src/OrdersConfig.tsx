import { OrdersStatusSettings } from "./OrdersStatusSettings";
import { OrdersAddressMapping } from "./OrdersAddressMapping";
import { OrdersReservationMapping } from "./OrdersReservationMapping";
import { OrdersNativePolicy, nativePolicyValid } from "./OrdersNativePolicy";
import { useRef, useState } from "react";
import { useOrdersMetadata } from "./useOrdersMetadata";
import { post } from "./api";
import { OrdersMetadataSetup } from "./OrdersMetadataSetup";
import { Icon } from "./UI";
import {
  settingsSections,
  type SettingsSection,
  SettingsHelp,
  automationFieldLabel,
} from "./OrdersSettingsParts";
import {
  OrdersWarehouseSettings,
  WarehouseSummary,
} from "./OrdersWarehouseSettings";
import { useOrdersDraftGuard } from "./useOrdersDraftGuard";
import "./orders-settings.css";
import {
  orderScopeBody,
  orderRequestError,
  sameOrderScope,
  type OrderScope,
  type OrdersConfigData,
  type OrderReference,
  type OrderMapping,
} from "./OrdersTypes";

const mappings: (OrderMapping & { label: string })[] = [
  {
    source: "posting_number",
    target: "name",
    label: "Номер отправления → название заказа",
  },
  {
    source: "times.created_at",
    target: "moment",
    label: "Дата создания Ozon → дата заказа",
  },
  {
    source: "positions.quantity",
    target: "positions.quantity",
    label: "Количество → количество в позициях",
  },
  {
    source: "positions.price",
    target: "positions.price",
    label: "Цена продавца → цена в позициях",
  },
];
const extraSources: { source: string; label: string; name?: boolean }[] = [
  { source: "order_number", label: "Номер заказа Ozon", name: true },
  { source: "substatus", label: "Подстатус Ozon" },
  {
    source: "parent_posting_number",
    label: "Номер родительского отправления",
  },
  {
    source: "child_posting_numbers",
    label: "Номера дочерних отправлений",
  },
  { source: "financial.order_total", label: "Стоимость заказа Ozon" },
  { source: "financial.seller_discount", label: "Скидка продавца" },
  { source: "financial.marketplace_discount", label: "Скидка маркетплейса" },
  { source: "financial.commission", label: "Комиссия Ozon" },
  { source: "financial.payout", label: "Выплата Ozon" },
  { source: "analytics.region", label: "Регион доставки" },
  { source: "analytics.city", label: "Город доставки" },
  { source: "analytics.delivery_type", label: "Тип доставки" },
  { source: "delivery.provider_name", label: "Служба доставки" },
  { source: "delivery.tracking_number", label: "Трек-номер" },
  { source: "delivery.destination_type", label: "Тип пункта назначения" },
  { source: "delivery.pickup_point_id", label: "ID пункта выдачи" },
  { source: "delivery.window_from", label: "Начало интервала доставки" },
  { source: "delivery.window_to", label: "Конец интервала доставки" },
];
const piiSources = [
  { source: "customer.name", label: "Имя покупателя" },
  { source: "customer.phone", label: "Телефон покупателя" },
  { source: "customer.email", label: "Электронная почта покупателя" },
] as const;
const reverseActions: { action: string; label: string }[] = [
  { action: "posting_pack", label: "Передать сборку в Ozon" },
  { action: "posting_ship", label: "Передать статус «В доставке»" },
  { action: "posting_deliver", label: "Передать статус «Доставлено»" },
  { action: "posting_cancel", label: "Отменить отправление" },
];
type ReferenceKey =
  | "organization_id"
  | "agent_id"
  | "state_id"
  | "sales_channel_id"
  | "currency_id";
function independentReadConfig(config: OrdersConfigData): OrdersConfigData {
  return {
    ...config,
    read_configured: true,
    read_enabled: config.read_configured
      ? config.read_enabled === true
      : config.read_enabled === true || config.enabled,
  };
}
export function OrdersConfig({
  scope,
  config,
  onSaved,
  onDirty,
  moyLabel = "Подключённый аккаунт",
  section = "general",
  onSection = () => {},
  ozonLabel = "Подключённый аккаунт",
}: {
  scope: OrderScope;
  config: OrdersConfigData;
  onSaved: (config: OrdersConfigData) => void;
  onDirty: (dirty: boolean) => void;
  moyLabel?: string;
  expanded?: boolean;
  section?: SettingsSection;
  onSection?: (section: SettingsSection) => void;
  ozonLabel?: string;
}) {
  const [draft, setDraft] = useState(() => independentReadConfig(config));
  const [dirty, setDirty] = useState(false);
  const [consent, setConsent] = useState(false);
  const [automationConsent, setAutomationConsent] = useState(false);
  const [automatic, setAutomatic] = useState(
    !!config.automation &&
      !config.automation.revoked_at &&
      (config.automation.perpetual === true ||
        Date.parse(config.automation.expires_at ?? "") > Date.now()),
  );
  const [permanent, setPermanent] = useState(
    config.automation?.perpetual === true,
  );
  const [actions, setActions] = useState(
    config.automation?.actions ?? ["mirror_create", "mirror_update"],
  );
  const [automaticFields, setAutomaticFields] = useState(
    config.automation?.fields ?? [],
  );
  const [busy, setBusy] = useState(false);
  const [setupBusy, setSetupBusy] = useState(false);
  const [error, setError] = useState("");
  const lock = useRef(false);
  const saveIntent = useRef<{ hash: string; key: string } | null>(null);
  const metadata = useOrdersMetadata(scope, true);
  useOrdersDraftGuard(dirty || busy || setupBusy);
  const permanentConsentNeeded =
    automatic &&
    permanent &&
    (!config.automation?.perpetual ||
      !!config.automation.revoked_at ||
      actions.some((action) => !config.automation?.actions.includes(action)) ||
      automaticFields.some(
        (field) => !config.automation?.fields.includes(field),
      ));
  function cancel() {
    if (!dirty || !window.confirm("Отменить несохранённые изменения?")) return;
    setDraft(independentReadConfig(config));
    setAutomatic(
      !!config.automation &&
        !config.automation.revoked_at &&
        (config.automation.perpetual === true ||
          Date.parse(config.automation.expires_at ?? "") > Date.now()),
    );
    setPermanent(config.automation?.perpetual === true);
    setActions(
      config.automation?.actions ?? ["mirror_create", "mirror_update"],
    );
    setAutomaticFields(config.automation?.fields ?? []);
    setAutomationConsent(false);
    setConsent(false);
    setDirty(false);
    onDirty(false);
    setError("");
  }
  function change(next: OrdersConfigData) {
    setDraft(next);
    setDirty(true);
    onDirty(true);
    setAutomationConsent(false);
    setError("");
  }
  function automationChange() {
    setPermanent(true);
    setDirty(true);
    onDirty(true);
    setAutomationConsent(false);
  }
  const mappingFor = (source: string) =>
    draft.field_mappings.find((item) => item.source === source);
  function setSourceMapping(source: string, target: string, attributeId = "") {
    const rest = draft.field_mappings.filter((item) => item.source !== source);
    change({
      ...draft,
      field_mappings:
        target === ""
          ? rest
          : [
              ...rest,
              {
                source,
                target,
                ...(target === "attribute"
                  ? { attribute_id: attributeId }
                  : {}),
              },
            ],
    });
  }
  const textAttributes = (metadata.data?.attributes ?? []).filter(
    (item) => item.type === "string" || item.type === "text",
  );
  const piiRetentionValid =
    !draft.pii_enabled ||
    (draft.pii_retention_seconds >= 60 &&
      draft.pii_retention_seconds <= 30 * 86400);
  const mappingsValid = draft.field_mappings.every(
    (item) =>
      item.source !== "" &&
      item.target !== "" &&
      (item.target !== "attribute" || !!item.attribute_id),
  );
  const reverseStates = (draft.reverse_status_mappings ?? []).map(
    (item) => item.state_id,
  );
  const reverseValid =
    (draft.reverse_status_mappings ?? []).every(
      (item) => item.state_id !== "" && item.action !== "",
    ) && new Set(reverseStates).size === reverseStates.length;
  const references: {
    key: ReferenceKey;
    label: string;
    options: OrderReference[];
  }[] = [
    {
      key: "organization_id",
      label: "Организация",
      options: metadata.data?.organizations ?? [],
    },
    {
      key: "agent_id",
      label: "Контрагент",
      options: metadata.data?.agents ?? [],
    },
    {
      key: "state_id",
      label: "Статус нового заказа",
      options: metadata.data?.states ?? [],
    },
    {
      key: "sales_channel_id",
      label: "Канал продаж",
      options: metadata.data?.sales_channels ?? [],
    },
    {
      key: "currency_id",
      label: "Валюта",
      options: (metadata.data?.currencies ?? []).map((item) => ({
        id: item.id,
        name: item.iso_code,
      })),
    },
  ];
  const enabledNeedsConsent = draft.enabled && !config.enabled;
  const referencesUnchanged =
    config.enabled &&
    references.every((ref) => draft[ref.key] === config[ref.key]);
  const referencesValid =
    referencesUnchanged ||
    references.every(
      (ref) =>
        !!draft[ref.key] &&
        ref.options.some((option) => option.id === draft[ref.key]),
    );
  const automaticAllowed =
    draft.enabled && config.publication_available === true;
  const automaticOptions = [
    ...draft.field_mappings.filter(
      (mapping, index, all) =>
        all.findIndex((item) => item.target === mapping.target) === index,
    ),
    ...automaticFields
      .filter(
        (field) =>
          field !== "state" &&
          !draft.field_mappings.some((mapping) => mapping.target === field),
      )
      .map((target) => ({ source: target, target })),
    { source: "status", target: "state" },
  ];
  const statusKeys = draft.status_mappings.map((item) =>
    JSON.stringify([item.status, item.substatus]),
  );
  const statusesValid =
    draft.status_mappings.every((item) => item.status && item.state_id) &&
    new Set(statusKeys).size === statusKeys.length;
  const missingReference = !referencesValid
    ? references.find(
        (ref) =>
          !draft[ref.key] ||
          !ref.options.some((option) => option.id === draft[ref.key]),
      )
    : undefined;
  const validationReason =
    draft.enabled && missingReference
      ? `Выберите поле «${missingReference.label}» в разделе «Общие».`
      : draft.enabled && !draft.field_mappings.length
        ? "Выберите данные для передачи в разделе «Данные заказа»."
        : !mappingsValid
          ? "Выберите целевое дополнительное поле для каждого правила."
          : !statusesValid
            ? "Заполните соответствия статусов без повторов в разделе «Статусы и действия»."
            : !reverseValid
              ? "Проверьте обратные предложения: состояние и действие обязательны, повторы не допускаются."
              : !piiRetentionValid
                ? "Проверьте срок хранения данных покупателя."
                : !nativePolicyValid(draft.native_documents)
                  ? "Проверьте разрешения складских документов в разделе «Дополнительно»."
                  : enabledNeedsConsent && !consent
                    ? "Подтвердите разрешение передачи в разделе «Общие»."
                    : automatic && (!actions.length || !automaticFields.length)
                      ? "Выберите действия и данные в разделе «Автоматизация»."
                      : permanentConsentNeeded && !automationConsent
                        ? "Подтвердите постоянную автоматизацию и выбранные права в разделе «Автоматизация»."
                        : "";
  const saveDisabled =
    busy ||
    setupBusy ||
    !dirty ||
    (enabledNeedsConsent && !consent) ||
    (draft.enabled && (!referencesValid || !draft.field_mappings.length)) ||
    !mappingsValid ||
    !piiRetentionValid ||
    !reverseValid ||
    !statusesValid ||
    !nativePolicyValid(draft.native_documents) ||
    (automatic &&
      (!automaticAllowed ||
        (permanentConsentNeeded && !automationConsent) ||
        !actions.length ||
        !automaticFields.length));
  async function save() {
    if (lock.current || saveDisabled) return;
    lock.current = true;
    setBusy(true);
    setError("");
    try {
      const {
        publication_available: available,
        read_enabled: readEnabled,
        read_configured: readConfigured,
        ...editable
      } = draft;
      const payload = {
        ...orderScopeBody(scope),
        expected_revision: config.revision,
        automation_consent: automationConsent,
        read_enabled: readConfigured ? readEnabled === true : draft.enabled,
        config: {
          ...editable,
          test_enabled: draft.enabled && available === true,
          automation: automatic
            ? {
                ...(config.automation ?? {}),
                actions,
                fields: automaticFields,
                perpetual: permanent,
                expires_at: permanent
                  ? undefined
                  : config.automation?.expires_at,
                revoked_at: undefined,
              }
            : null,
        },
      };
      const hash = JSON.stringify(payload);
      if (saveIntent.current?.hash !== hash)
        saveIntent.current = { hash, key: crypto.randomUUID() };
      const value = await post<OrdersConfigData>(
        "/api/orders/config",
        { ...payload, idempotency_key: saveIntent.current.key },
        AbortSignal.timeout(30000),
      );
      if (!sameOrderScope(value.scope, scope))
        throw new Error("config scope mismatch");
      setDirty(false);
      onDirty(false);
      onSaved({
        ...value,
        publication_available: config.publication_available,
      });
    } catch (error) {
      setError(orderRequestError(error));
    } finally {
      lock.current = false;
      setBusy(false);
    }
  }
  return (
    <form
      className="order-settings-form"
      onSubmit={(event) => {
        event.preventDefault();
        void save();
      }}
    >
      <div className="settings-workspace">
        <nav className="settings-nav" aria-label="Разделы настроек заказов">
          {settingsSections.map((item) => (
            <button
              type="button"
              className="settings-nav-button"
              key={item.id}
              aria-current={section === item.id ? "page" : undefined}
              onClick={() => onSection(item.id)}
            >
              <Icon name={item.icon} />
              {item.label}
            </button>
          ))}
        </nav>
        <label className="settings-mobile-nav">
          Раздел настроек
          <select
            value={section}
            onChange={(event) =>
              onSection(event.target.value as SettingsSection)
            }
          >
            {settingsSections.map((item) => (
              <option key={item.id} value={item.id}>
                {item.label}
              </option>
            ))}
          </select>
        </label>
        <div className="settings-content">
          <section
            hidden={section !== "general"}
            aria-labelledby="settings-general"
          >
            <h2 id="settings-general">Как работают ваши заказы</h2>
            <label className="orders-check">
              <input
                type="checkbox"
                aria-label="Получать отправления из Ozon"
                checked={
                  draft.read_configured
                    ? draft.read_enabled === true
                    : draft.enabled
                }
                disabled={busy}
                onChange={(event) =>
                  change({
                    ...draft,
                    read_enabled: event.target.checked,
                    read_configured: true,
                  })
                }
              />
              <Icon name="refresh" />
              <span>
                <strong>Получать отправления из Ozon</strong>
                <small aria-hidden="true">
                  STYK регулярно обновляет список заказов
                </small>
              </span>
            </label>
            <label className="orders-check">
              <input
                type="checkbox"
                aria-label="Передавать заказы в МойСклад"
                checked={draft.enabled}
                disabled={busy}
                onChange={(event) => {
                  change({ ...draft, enabled: event.target.checked });
                  setConsent(false);
                  if (!event.target.checked) setAutomatic(false);
                }}
              />
              <Icon name="arrow" />
              <span>
                <strong>Передавать заказы в МойСклад</strong>
                <small aria-hidden="true">
                  Создание и обновление — после проверки или с вашим разрешением
                </small>
              </span>
            </label>
            {enabledNeedsConsent && (
              <label className="orders-check orders-warning">
                <input
                  type="checkbox"
                  checked={consent}
                  onChange={(event) => setConsent(event.target.checked)}
                />
                Разрешаю создание и обновление заказов после отдельного
                подтверждения или по явно заданному разрешению автоматизации.
              </label>
            )}
            {metadata.isLoading && (
              <p role="status">Читаем справочники МойСклад…</p>
            )}
            {metadata.isError && (
              <p className="orders-error" role="alert">
                Не удалось прочитать справочники МойСклад.{" "}
                <button
                  className="text-action"
                  type="button"
                  onClick={() => void metadata.refetch()}
                >
                  Обновить справочники
                </button>
              </p>
            )}
            <p className="settings-info">
              <Icon name="error" />
              Включение передачи само по себе не отправляет заказы без
              подтверждения.
            </p>
            <h2>Куда попадут заказы</h2>{" "}
            <div className="orders-form-grid">
              {[...references]
                .sort(
                  (a, b) =>
                    [
                      "organization_id",
                      "sales_channel_id",
                      "agent_id",
                      "currency_id",
                      "state_id",
                    ].indexOf(a.key) -
                    [
                      "organization_id",
                      "sales_channel_id",
                      "agent_id",
                      "currency_id",
                      "state_id",
                    ].indexOf(b.key),
                )
                .map((ref) => (
                  <label key={ref.key}>
                    {ref.label}
                    <select
                      value={draft[ref.key] ?? ""}
                      disabled={busy || !metadata.data}
                      onChange={(event) =>
                        change({ ...draft, [ref.key]: event.target.value })
                      }
                    >
                      <option value="">Выберите из МойСклад</option>
                      {draft[ref.key] &&
                        !ref.options.some(
                          (option) => option.id === draft[ref.key],
                        ) && (
                          <option value={draft[ref.key]}>
                            Сохранённая ссылка недоступна
                          </option>
                        )}
                      {ref.options.map((option) => (
                        <option value={option.id} key={option.id}>
                          {option.name}
                        </option>
                      ))}
                    </select>
                  </label>
                ))}
            </div>
            <WarehouseSummary
              scope={scope}
              onOpen={() => onSection("warehouses")}
            />
          </section>
          <section
            hidden={section !== "warehouses"}
            aria-labelledby="settings-warehouses"
          >
            <h2 id="settings-warehouses">Куда попадут заказы</h2>
            <p>Склад выбирается отдельно для каждого отправления Ozon.</p>
            <OrdersWarehouseSettings scope={scope} />
          </section>
          <section
            hidden={section !== "fields"}
            aria-labelledby="settings-fields"
          >
            <h2 id="settings-fields">Какие данные передавать</h2>
            <p>Отметьте, что должно появиться в заказе МойСклад.</p>
            <div className="settings-card">
              <h3>Основные поля</h3>
              <div className="settings-field-heading">
                <span>Данные Ozon</span>
                <span>В МойСклад</span>
                <span>Передавать</span>
              </div>
              {mappings.map((mapping) => (
                <label className="settings-field-row" key={mapping.target}>
                  <span>
                    <Icon
                      name={mapping.target === "moment" ? "history" : "catalog"}
                    />
                    {mapping.label.split(" → ")[0]}
                  </span>
                  <span>
                    <Icon name="arrow" />
                    {mapping.label.split(" → ")[1]}
                  </span>
                  <input
                    className="settings-switch"
                    type="checkbox"
                    aria-label={mapping.label}
                    checked={draft.field_mappings.some(
                      (item) => item.target === mapping.target,
                    )}
                    disabled={busy}
                    onChange={(event) =>
                      change({
                        ...draft,
                        field_mappings: event.target.checked
                          ? [
                              ...draft.field_mappings.filter(
                                (item) => item.target !== mapping.target,
                              ),
                              {
                                source: mapping.source,
                                target: mapping.target,
                              },
                            ]
                          : draft.field_mappings.filter(
                              (item) => item.target !== mapping.target,
                            ),
                      })
                    }
                  />
                </label>
              ))}
            </div>
            <p className="settings-info">
              <Icon name="error" />
              Изменения применяются только к выбранным полям. Учётные даты
              сохраняются с точностью до минуты.
            </p>
            <div className="settings-card">
              <h3>Остатки и проведение</h3>{" "}
              <OrdersReservationMapping
                compact
                mappings={draft.field_mappings}
                disabled={busy}
                onChange={(field_mappings) =>
                  change({ ...draft, field_mappings })
                }
              />
              <p className="settings-info">
                <Icon name="error" />
                Проведение — это учёт в МойСклад, а не отправка покупателю.
              </p>
            </div>{" "}
            <fieldset>
              <legend>Персональные данные покупателя</legend>
              <p className="orders-note">
                Значения не сохраняются в предпросмотре, журнале и снимках. Они
                запрашиваются заново на момент отправки и сверки.
              </p>
              <label className="orders-check">
                <input
                  type="checkbox"
                  checked={draft.pii_enabled}
                  disabled={busy}
                  onChange={(event) => {
                    const enabled = event.target.checked;
                    change({
                      ...draft,
                      pii_enabled: enabled,
                      pii_retention_seconds: enabled
                        ? draft.pii_retention_seconds || 86400
                        : 0,
                      field_mappings: enabled
                        ? draft.field_mappings
                        : draft.field_mappings.filter(
                            (item) => !item.source.startsWith("customer."),
                          ),
                    });
                  }}
                />
                Передавать персональные данные покупателя
              </label>
              {draft.pii_enabled && (
                <>
                  <label>
                    Срок хранения защищённой копии
                    <select
                      value={draft.pii_retention_seconds || 86400}
                      disabled={busy}
                      onChange={(event) =>
                        change({
                          ...draft,
                          pii_retention_seconds: Number(event.target.value),
                        })
                      }
                    >
                      <option value={3600}>1 час</option>
                      <option value={86400}>1 сутки</option>
                      <option value={604800}>7 суток</option>
                      <option value={2592000}>30 суток</option>
                    </select>
                  </label>
                  {piiSources.map((item) => (
                    <label key={item.source}>
                      {item.label} → дополнительное поле МойСклад
                      <select
                        value={mappingFor(item.source)?.attribute_id ?? ""}
                        disabled={!metadata.data || busy}
                        onChange={(event) =>
                          setSourceMapping(
                            item.source,
                            event.target.value ? "attribute" : "",
                            event.target.value,
                          )
                        }
                      >
                        <option value="">Не передавать</option>
                        {textAttributes.map((attribute) => (
                          <option key={attribute.id} value={attribute.id}>
                            {attribute.name}
                          </option>
                        ))}
                      </select>
                    </label>
                  ))}
                  <OrdersAddressMapping
                    value={draft.field_mappings}
                    disabled={busy}
                    onChange={(field_mappings) =>
                      change({ ...draft, field_mappings })
                    }
                  />
                </>
              )}
            </fieldset>
          </section>
          <section
            hidden={section !== "statuses"}
            aria-labelledby="settings-statuses"
          >
            <h2 id="settings-statuses">Статусы и действия</h2>
            <p>
              Настройте, как STYK сопоставляет этапы заказа в двух сервисах.
            </p>
            <div className="settings-card">
              <h3>Статусы Ozon → МойСклад</h3>
              <p>
                При изменении статуса Ozon выбирается состояние заказа в
                МойСклад.
              </p>
              <OrdersStatusSettings
                draft={draft}
                metadata={metadata.data}
                disabled={busy}
                onChange={change}
              />
            </div>
            <div className="settings-card">
              <h3>
                Подсказки для действий в Ozon{" "}
                <span className="settings-badge">Только предложения</span>
              </h3>
              <p className="settings-info">
                <Icon name="error" />
                Изменения в МойСклад никогда не запускают действие в Ozon сами.
                Сначала вы проверяете и подтверждаете его.
              </p>{" "}
              {(draft.reverse_status_mappings ?? []).map((mapping, index) => (
                <div className="orders-status-mapping" key={index}>
                  <label>
                    Состояние МойСклад
                    <select
                      value={mapping.state_id}
                      onChange={(event) =>
                        change({
                          ...draft,
                          reverse_status_mappings: (
                            draft.reverse_status_mappings ?? []
                          ).map((item, i) =>
                            i === index
                              ? { ...item, state_id: event.target.value }
                              : item,
                          ),
                        })
                      }
                    >
                      <option value="">Выберите состояние</option>
                      {(metadata.data?.states ?? []).map((item) => (
                        <option value={item.id} key={item.id}>
                          {item.name}
                        </option>
                      ))}
                    </select>
                  </label>
                  <label>
                    Предлагаемое действие Ozon
                    <select
                      value={mapping.action}
                      onChange={(event) =>
                        change({
                          ...draft,
                          reverse_status_mappings: (
                            draft.reverse_status_mappings ?? []
                          ).map((item, i) =>
                            i === index
                              ? { ...item, action: event.target.value }
                              : item,
                          ),
                        })
                      }
                    >
                      <option value="">Выберите действие</option>
                      {reverseActions.map((item) => (
                        <option value={item.action} key={item.action}>
                          {item.label}
                        </option>
                      ))}
                    </select>
                  </label>
                  <button
                    type="button"
                    className="secondary"
                    aria-label={`Удалить обратное предложение ${index + 1}`}
                    onClick={() =>
                      change({
                        ...draft,
                        reverse_status_mappings: (
                          draft.reverse_status_mappings ?? []
                        ).filter((_, i) => i !== index),
                      })
                    }
                  >
                    Удалить
                  </button>
                </div>
              ))}
              <button
                className="secondary"
                type="button"
                onClick={() =>
                  change({
                    ...draft,
                    reverse_status_mappings: [
                      ...(draft.reverse_status_mappings ?? []),
                      { state_id: "", action: "" },
                    ],
                  })
                }
              >
                Добавить обратное предложение
              </button>
            </div>
          </section>
          <section
            hidden={section !== "automation"}
            aria-labelledby="settings-automation"
          >
            <h2 id="settings-automation">Автоматическая передача</h2>
            <p>
              STYK может обновлять заказы без ручного подтверждения — только в
              рамках вашего разрешения.
            </p>
            <div className="settings-automation-status" data-active={automatic}>
              <Icon name="refresh" />
              <div>
                <h3>
                  {automatic
                    ? "Автоматическая передача включена"
                    : "Автоматическая передача выключена"}
                </h3>
                <p>
                  {automatic
                    ? permanent
                      ? "Работает постоянно, пока вы её не выключите"
                      : "Ранее выданное разрешение действует в прежних пределах"
                    : "Включите передачу и выберите разрешённые действия и данные"}
                </p>
              </div>
              <label className="settings-automation-toggle">
                <input
                  type="checkbox"
                  className="settings-switch"
                  checked={automatic}
                  disabled={!automatic && !automaticAllowed}
                  onChange={(event) => {
                    setAutomatic(event.target.checked);
                    if (event.target.checked) setPermanent(true);
                    automationChange();
                  }}
                />
                <span className="sr-only">Автоматическая передача</span>
              </label>
            </div>
            {config.automation &&
              !config.automation.perpetual &&
              !permanent && (
                <div className="settings-info">
                  Ранее выданное разрешение имеет ограниченный срок. Для
                  постоянной работы нужно новое согласие.
                  <button
                    type="button"
                    className="text-action"
                    onClick={() => {
                      setPermanent(true);
                      setAutomatic(true);
                      automationChange();
                    }}
                  >
                    Включить постоянную автоматизацию
                  </button>
                </div>
              )}
            {!automaticAllowed && (
              <p className="settings-info">
                Сначала включите передачу в разделе «Общие» и проверьте
                подключение аккаунтов.
              </p>
            )}

            {automatic && (
              <button
                type="button"
                className="secondary"
                onClick={() => {
                  setAutomatic(false);
                  automationChange();
                }}
              >
                Отключить автоматизацию
              </button>
            )}
            {automatic && (
              <>
                <div className="settings-permissions">
                  <h3>Что STYK делает автоматически</h3>
                  <div className="settings-permissions-grid">
                    {" "}
                    <fieldset>
                      <legend>Действия</legend>
                      {[
                        ["mirror_create", "Создавать заказы"],
                        ["mirror_update", "Обновлять заказы"],
                      ].map(([value, label]) => (
                        <label className="orders-check" key={value}>
                          <input
                            type="checkbox"
                            checked={actions.includes(value)}
                            onChange={(event) => {
                              setActions(
                                event.target.checked
                                  ? [...actions, value]
                                  : actions.filter((item) => item !== value),
                              );
                              automationChange();
                            }}
                          />
                          {label}
                        </label>
                      ))}
                    </fieldset>
                    <fieldset>
                      <legend>Данные</legend>
                      {automaticOptions.map((mapping) => (
                        <label className="orders-check" key={mapping.target}>
                          <input
                            type="checkbox"
                            aria-label={
                              mappings.find(
                                (item) => item.target === mapping.target,
                              )?.label ??
                              (mapping.target === "state"
                                ? "Состояние заказа МойСклад"
                                : automationFieldLabel(mapping.target))
                            }
                            checked={automaticFields.includes(mapping.target)}
                            onChange={(event) => {
                              setAutomaticFields(
                                event.target.checked
                                  ? [...automaticFields, mapping.target]
                                  : automaticFields.filter(
                                      (item) => item !== mapping.target,
                                    ),
                              );
                              automationChange();
                            }}
                          />
                          {automationFieldLabel(mapping.target)}
                        </label>
                      ))}
                    </fieldset>
                  </div>
                </div>{" "}
                {permanentConsentNeeded && (
                  <label className="orders-check orders-warning">
                    <input
                      type="checkbox"
                      checked={automationConsent}
                      onChange={(event) =>
                        setAutomationConsent(event.target.checked)
                      }
                    />
                    Разрешаю автоматическое выполнение выбранных действий с
                    выбранными полями постоянно, пока я не отключу
                    автоматизацию.
                  </label>
                )}
              </>
            )}
            <div className="settings-card">
              <h3>Включено для пары аккаунтов</h3>
              <p>
                Ozon: <strong>{ozonLabel}</strong> → МойСклад:{" "}
                <strong>{moyLabel}</strong>
              </p>
              <p>
                {dirty
                  ? "Изменения вступят в силу после сохранения"
                  : automatic
                    ? permanent
                      ? "Сохраняется без срока действия"
                      : "Сохраняется в прежних пределах"
                    : "Автоматизация отключена"}
              </p>
            </div>
            <p className="settings-warning">
              <Icon name="error" />
              Если связь со складом или товаром не подтверждена, STYK не
              отправит затронутый заказ до устранения проблемы.
            </p>
            <button
              type="button"
              className="secondary"
              onClick={() => onSection("advanced")}
            >
              Складские документы и специальные действия{" "}
              <Icon name="chevron-right" />
            </button>
          </section>
          <section
            hidden={section !== "advanced"}
            aria-labelledby="settings-advanced"
          >
            <h2 id="settings-advanced">Дополнительно</h2>
            <p>Дополнительные данные, служебные поля и параметры проверки.</p>
            <details>
              <summary>Дополнительные поля Ozon</summary>
              <div className="orders-detail-body">
                {" "}
                <label>
                  Номер отправления → дополнительное поле МойСклад
                  <select
                    value={
                      draft.field_mappings.find(
                        (item) =>
                          item.target === "attribute" &&
                          item.source === "posting_number",
                      )?.attribute_id ?? ""
                    }
                    disabled={!metadata.data || busy}
                    onChange={(event) =>
                      change({
                        ...draft,
                        field_mappings: [
                          ...draft.field_mappings.filter(
                            (item) =>
                              !(
                                item.target === "attribute" &&
                                item.source === "posting_number"
                              ),
                          ),
                          ...(event.target.value
                            ? [
                                {
                                  source: "posting_number",
                                  target: "attribute",
                                  attribute_id: event.target.value,
                                },
                              ]
                            : []),
                        ],
                      })
                    }
                  >
                    <option value="">Не передавать</option>
                    {(metadata.data?.attributes ?? [])
                      .filter(
                        (item) =>
                          item.type === "string" || item.type === "text",
                      )
                      .map((item) => (
                        <option key={item.id} value={item.id}>
                          {item.name}
                        </option>
                      ))}
                  </select>
                </label>
                <p>
                  Дополнительные поля Ozon можно передать в название, описание
                  или дополнительное поле МойСклад. Значения не объединяются в
                  описание автоматически.
                </p>
                {extraSources.map((item) => (
                  <div className="orders-status-mapping" key={item.source}>
                    <span>{item.label}</span>
                    <select
                      value={mappingFor(item.source)?.target ?? ""}
                      disabled={busy}
                      onChange={(event) =>
                        setSourceMapping(item.source, event.target.value)
                      }
                    >
                      <option value="">Не передавать</option>
                      {item.name && (
                        <option value="name">Название заказа</option>
                      )}
                      <option value="description">Описание</option>
                      <option value="attribute">Дополнительное поле</option>
                    </select>
                    {mappingFor(item.source)?.target === "attribute" && (
                      <select
                        value={mappingFor(item.source)?.attribute_id ?? ""}
                        disabled={!metadata.data || busy}
                        onChange={(event) =>
                          setSourceMapping(
                            item.source,
                            "attribute",
                            event.target.value,
                          )
                        }
                      >
                        <option value="">Выберите поле</option>
                        {textAttributes.map((attribute) => (
                          <option key={attribute.id} value={attribute.id}>
                            {attribute.name}
                          </option>
                        ))}
                      </select>
                    )}
                  </div>
                ))}
              </div>
            </details>{" "}
            <OrdersMetadataSetup
              key={JSON.stringify(scope)}
              scope={scope}
              available={config.publication_available === true}
              targetLabel={moyLabel}
              mappings={draft.field_mappings}
              disabled={busy}
              onBusy={setSetupBusy}
              onMappings={(field_mappings) =>
                change({ ...draft, field_mappings })
              }
            />
            <OrdersNativePolicy
              value={draft.native_documents}
              disabled={busy}
              onChange={(native_documents) =>
                change({ ...draft, native_documents })
              }
            />
            <div className="settings-card">
              <h3>Параметры проверки</h3>{" "}
              <div className="orders-form-grid">
                <label>
                  Интервал проверки, секунд
                  <input
                    type="number"
                    min={30}
                    max={86400}
                    required
                    value={draft.interval_seconds || 300}
                    onChange={(event) =>
                      change({
                        ...draft,
                        interval_seconds: Number(event.target.value),
                      })
                    }
                  />
                </label>
                <label>
                  Свежесть источника, секунд
                  <input
                    type="number"
                    min={1}
                    max={86400}
                    required
                    value={draft.source_freshness_seconds || 300}
                    onChange={(event) =>
                      change({
                        ...draft,
                        source_freshness_seconds: Number(event.target.value),
                      })
                    }
                  />
                </label>
                <label>
                  Срок предпросмотра, секунд
                  <input
                    type="number"
                    min={1}
                    max={900}
                    required
                    value={draft.preview_ttl_seconds || 300}
                    onChange={(event) =>
                      change({
                        ...draft,
                        preview_ttl_seconds: Number(event.target.value),
                      })
                    }
                  />
                </label>
              </div>
            </div>
          </section>
        </div>
      </div>
      <SettingsHelp section={section} scope={scope} />
      <footer className="settings-footer">
        <div role="status" className="settings-save-status" data-dirty={dirty}>
          <span />
          {busy
            ? "Сохраняем настройки…"
            : dirty
              ? "Есть несохранённые изменения"
              : "Настройки сохранены"}
        </div>
        {dirty && validationReason && (
          <p className="settings-validation" role="status">
            {validationReason}
          </p>
        )}
        {error && (
          <p className="orders-error" role="alert">
            {error}{" "}
            {error.includes("измен") &&
              "Настройки могли измениться в другой вкладке. Обновите страницу и проверьте изменения перед повторным сохранением."}
          </p>
        )}
        <div className="settings-footer-actions">
          <button
            type="button"
            className="secondary"
            disabled={busy || setupBusy || !dirty}
            onClick={cancel}
          >
            Отменить
          </button>
          <button type="submit" disabled={saveDisabled}>
            {busy ? "Сохраняем…" : "Сохранить настройки"}
          </button>
        </div>
      </footer>
    </form>
  );
}
