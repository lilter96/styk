import { useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api, post } from "./api";
import {
  ordersURL,
  orderScopeBody,
  orderRequestError,
  sameOrderScope,
  orderDate,
  postingStatusLabels,
  type OrderScope,
  type OrdersConfigData,
  type OrdersMetadata,
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
  {
    source: "constant.zero",
    target: "positions.reserve",
    label: "Нулевой резерв в позициях",
  },
  {
    source: "constant.false",
    target: "applicable",
    label: "Не проводить заказ в МойСклад",
  },
];
type ReferenceKey =
  | "organization_id"
  | "agent_id"
  | "store_id"
  | "state_id"
  | "sales_channel_id"
  | "currency_id";
export function OrdersConfig({
  scope,
  config,
  onSaved,
  onDirty,
}: {
  scope: OrderScope;
  config: OrdersConfigData;
  onSaved: (config: OrdersConfigData) => void;
  onDirty: (dirty: boolean) => void;
}) {
  const [draft, setDraft] = useState(config);
  const [dirty, setDirty] = useState(false);
  const [consent, setConsent] = useState(false);
  const [automationConsent, setAutomationConsent] = useState(false);
  const [automatic, setAutomatic] = useState(
    !!config.automation &&
      !config.automation.revoked_at &&
      Date.parse(config.automation.expires_at) > Date.now(),
  );
  const [actions, setActions] = useState(
    config.automation?.actions ?? ["mirror_create", "mirror_update"],
  );
  const [automaticFields, setAutomaticFields] = useState(
    config.automation?.fields ?? [],
  );
  const [hours, setHours] = useState(1);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const lock = useRef(false);
  const metadata = useQuery({
    queryKey: ["orders-metadata", scope],
    queryFn: async ({ signal }) => {
      const value = await api<OrdersMetadata>(
        ordersURL("metadata", scope),
        signal,
      );
      if (!sameOrderScope(value.scope, scope))
        throw new Error("metadata scope mismatch");
      return value;
    },
    retry: false,
    staleTime: 300000,
    refetchOnWindowFocus: false,
  });
  function change(next: OrdersConfigData) {
    setDraft(next);
    setDirty(true);
    onDirty(true);
    setAutomationConsent(false);
    setError("");
  }
  function automationChange() {
    setDirty(true);
    onDirty(true);
    setAutomationConsent(false);
  }
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
      key: "store_id",
      label: "Склад МойСклад",
      options: metadata.data?.stores ?? [],
    },
    {
      key: "state_id",
      label: "Состояние заказа по умолчанию",
      options: metadata.data?.states ?? [],
    },
    {
      key: "sales_channel_id",
      label: "Канал продаж",
      options: metadata.data?.sales_channels ?? [],
    },
    {
      key: "currency_id",
      label: "Валюта заказа",
      options: (metadata.data?.currencies ?? []).map((item) => ({
        id: item.id,
        name: item.iso_code,
      })),
    },
  ];
  const enabledNeedsConsent = draft.enabled && !config.enabled;
  const referencesValid = references.every(
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
    { source: "status", target: "state" },
  ];
  const saveDisabled =
    busy ||
    !dirty ||
    (enabledNeedsConsent && !consent) ||
    (draft.enabled && (!referencesValid || !draft.field_mappings.length)) ||
    (automatic &&
      (!automaticAllowed ||
        !automationConsent ||
        !actions.length ||
        !automaticFields.length));
  async function save() {
    if (lock.current || saveDisabled) return;
    lock.current = true;
    setBusy(true);
    setError("");
    try {
      const { publication_available: available, ...editable } = draft;
      const value = await post<OrdersConfigData>(
        "/api/orders/config",
        {
          ...orderScopeBody(scope),
          expected_revision: config.revision,
          idempotency_key: crypto.randomUUID(),
          config: {
            ...editable,
            test_enabled: draft.enabled && available === true,
            automation: automatic
              ? {
                  actions,
                  fields: automaticFields,
                  expires_at: new Date(
                    Date.now() + hours * 3600000,
                  ).toISOString(),
                }
              : null,
          },
        },
        AbortSignal.timeout(30000),
      );
      if (!sameOrderScope(value.scope, scope))
        throw new Error("config scope mismatch");
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
    <details className="orders-section orders-settings" open={!config.revision}>
      <summary>
        <strong>Настройки передачи в МойСклад</strong>
        <span>{config.enabled ? "Включена" : "Выключена"}</span>
      </summary>
      <form
        onSubmit={(event) => {
          event.preventDefault();
          void save();
        }}
      >
        <p>
          Одна запись на отправление Ozon. Связи товаров и складов должны быть
          подтверждены заранее.
        </p>
        <label className="orders-check">
          <input
            type="checkbox"
            checked={draft.enabled}
            disabled={busy}
            onChange={(event) => {
              change({ ...draft, enabled: event.target.checked });
              setConsent(false);
              if (!event.target.checked) setAutomatic(false);
            }}
          />
          Разрешить передачу заказов в МойСклад
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
        <div className="orders-form-grid">
          {references.map((ref) => (
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
        <p className="orders-note">
          Контрагент выбирается из МойСклад. Персональные данные покупателя в
          этом разделе не отображаются.
        </p>
        <fieldset>
          <legend>Какие поля передавать</legend>
          {mappings.map((mapping) => (
            <label className="orders-check" key={mapping.target}>
              <input
                type="checkbox"
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
                          { source: mapping.source, target: mapping.target },
                        ]
                      : draft.field_mappings.filter(
                          (item) => item.target !== mapping.target,
                        ),
                  })
                }
              />
              {mapping.label}
            </label>
          ))}
        </fieldset>
        <details>
          <summary>Дополнительные поля и состояния</summary>
          <div className="orders-detail-body">
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
                    (item) => item.type === "string" || item.type === "text",
                  )
                  .map((item) => (
                    <option key={item.id} value={item.id}>
                      {item.name}
                    </option>
                  ))}
              </select>
            </label>
            <p>
              Каждый статус Ozon сопоставляется отдельно. Отмена, доставка и
              споры не образуют последовательную шкалу.
            </p>
            {draft.status_mappings.map((mapping, index) => (
              <div className="orders-status-mapping" key={index}>
                <label>
                  Статус Ozon
                  <input
                    value={mapping.status}
                    list="orders-statuses"
                    onChange={(event) =>
                      change({
                        ...draft,
                        status_mappings: draft.status_mappings.map((item, i) =>
                          i === index
                            ? { ...item, status: event.target.value }
                            : item,
                        ),
                      })
                    }
                  />
                </label>
                <label>
                  Подстатус (необязательно)
                  <input
                    value={mapping.substatus.value ?? ""}
                    onChange={(event) =>
                      change({
                        ...draft,
                        status_mappings: draft.status_mappings.map((item, i) =>
                          i === index
                            ? {
                                ...item,
                                substatus: event.target.value
                                  ? {
                                      presence: "value",
                                      value: event.target.value,
                                    }
                                  : { presence: "missing" },
                              }
                            : item,
                        ),
                      })
                    }
                  />
                </label>
                <label>
                  Состояние МойСклад
                  <select
                    value={mapping.state_id}
                    onChange={(event) =>
                      change({
                        ...draft,
                        status_mappings: draft.status_mappings.map((item, i) =>
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
                <button
                  type="button"
                  className="secondary"
                  aria-label={`Удалить соответствие статуса ${mapping.status || index + 1}`}
                  onClick={() =>
                    change({
                      ...draft,
                      status_mappings: draft.status_mappings.filter(
                        (_, i) => i !== index,
                      ),
                    })
                  }
                >
                  Удалить
                </button>
              </div>
            ))}
            <datalist id="orders-statuses">
              {Object.entries(postingStatusLabels).map(([value, label]) => (
                <option key={value} value={value}>
                  {label}
                </option>
              ))}
            </datalist>
            <button
              className="secondary"
              type="button"
              onClick={() =>
                change({
                  ...draft,
                  status_mappings: [
                    ...draft.status_mappings,
                    {
                      status: "",
                      substatus: { presence: "missing" },
                      state_id: "",
                    },
                  ],
                })
              }
            >
              Добавить соответствие статуса
            </button>
          </div>
        </details>
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
        <details>
          <summary>Автоматическая передача по ограниченному разрешению</summary>
          <div className="orders-detail-body">
            <p>
              Разрешение действует для этой пары аккаунтов и выбранных полей. По
              истечении срока новые изменения потребуют подтверждения.
            </p>
            {config.automation && (
              <p>
                Сохранённое разрешение до{" "}
                {orderDate(config.automation.expires_at)}.
              </p>
            )}
            <label className="orders-check">
              <input
                type="checkbox"
                checked={automatic}
                disabled={!automatic && !automaticAllowed}
                onChange={(event) => {
                  setAutomatic(event.target.checked);
                  automationChange();
                }}
              />
              Автоматически передавать выбранные поля
            </label>
            {automatic && (
              <>
                <fieldset>
                  <legend>Разрешённые действия</legend>
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
                  <legend>Разрешённые поля</legend>
                  {automaticOptions.map((mapping) => (
                    <label className="orders-check" key={mapping.target}>
                      <input
                        type="checkbox"
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
                      {mappings.find((item) => item.target === mapping.target)
                        ?.label ??
                        (mapping.target === "state"
                          ? "Состояние заказа МойСклад"
                          : mapping.target)}
                    </label>
                  ))}
                </fieldset>
                <label>
                  Срок разрешения
                  <select
                    value={hours}
                    onChange={(event) => {
                      setHours(Number(event.target.value));
                      automationChange();
                    }}
                  >
                    <option value={1}>1 час</option>
                    <option value={4}>4 часа</option>
                    <option value={12}>12 часов</option>
                  </select>
                </label>
                {dirty && (
                  <label className="orders-check orders-warning">
                    <input
                      type="checkbox"
                      checked={automationConsent}
                      onChange={(event) =>
                        setAutomationConsent(event.target.checked)
                      }
                    />
                    Разрешаю автоматическое выполнение выбранных действий с
                    выбранными полями в течение указанного срока.
                  </label>
                )}
              </>
            )}
          </div>
        </details>
        {error && (
          <p className="orders-error" role="alert">
            {error}
          </p>
        )}
        <div className="orders-actions">
          <button type="submit" disabled={saveDisabled}>
            {busy ? "Сохраняем настройки…" : "Сохранить настройки заказов"}
          </button>
          {dirty && <span>Есть несохранённые изменения</span>}
        </div>
      </form>
    </details>
  );
}
