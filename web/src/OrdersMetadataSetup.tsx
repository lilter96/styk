import { useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError, post } from "./api";
import {
  orderRequestError,
  orderScopeBody,
  ordersURL,
  sameOrderScope,
  type NativeScope,
  type OrderMapping,
  type OrderScope,
} from "./OrdersTypes";

export const orderServiceFields = [
  {
    unique_code: "styk-order-id",
    source: "commercial_order_id",
    label: "ID заказа Ozon",
  },
  { unique_code: "styk-order-status", source: "status", label: "Статус Ozon" },
  {
    unique_code: "styk-order-substatus",
    source: "substatus",
    label: "Подстатус Ozon",
  },
] as const;
export type OrdersMetadataRegistry = {
  scope: NativeScope;
  revision: number;
  attributes: { unique_code: string; attribute_id: string; type: string }[];
};
type SetupResult = {
  state: string;
  registry?: OrdersMetadataRegistry;
  intent_id?: string;
  code?: string;
};
type Intent = {
  key: string;
  state: "unknown" | "reconciled" | "denied" | "failed";
  message?: string;
};
type Intents = Record<string, Intent>;
const storageKey = (scope: OrderScope) =>
  `styk-orders-metadata-intents:${JSON.stringify(orderScopeBody(scope))}`;
function readIntents(scope: OrderScope): Intents {
  try {
    const value: unknown = JSON.parse(
      sessionStorage.getItem(storageKey(scope)) ?? "{}",
    );
    if (!value || typeof value !== "object") return {};
    return Object.fromEntries(
      orderServiceFields.flatMap((field) => {
        const item = (value as Record<string, unknown>)[field.unique_code];
        if (
          !item ||
          typeof item !== "object" ||
          !("key" in item) ||
          !("state" in item) ||
          typeof item.key !== "string" ||
          !["unknown", "reconciled", "denied", "failed"].includes(
            String(item.state),
          )
        )
          return [];
        return [[field.unique_code, item as Intent]];
      }),
    );
  } catch {
    return {};
  }
}
function verifiedAttribute(
  registry: OrdersMetadataRegistry | undefined,
  code: string,
) {
  return registry?.attributes?.find(
    (item) =>
      item.unique_code === code &&
      item.type === "string" &&
      /^[\da-f]{8}-(?:[\da-f]{4}-){3}[\da-f]{12}$/i.test(item.attribute_id),
  );
}
export function OrdersMetadataSetup({
  scope,
  available,
  targetLabel,
  mappings,
  disabled,
  onMappings,
  onBusy,
}: {
  scope: OrderScope;
  available: boolean;
  targetLabel: string;
  mappings: OrderMapping[];
  disabled: boolean;
  onMappings: (mappings: OrderMapping[]) => void;
  onBusy: (busy: boolean) => void;
}) {
  const client = useQueryClient();
  const [consent, setConsent] = useState(false);
  const [intents, setIntents] = useState<Intents>(() => readIntents(scope));
  const [busy, setBusy] = useState(false);
  const [storageError, setStorageError] = useState("");
  const lock = useRef(false);
  const registryKey = ["orders-metadata-registry", scope];
  const registry = useQuery({
    queryKey: registryKey,
    queryFn: async ({ signal }) => {
      const value = await api<OrdersMetadataRegistry>(
        ordersURL("metadata/registry", scope),
        signal,
      );
      if (!sameOrderScope(value.scope, scope))
        throw new Error("registry scope mismatch");
      return value;
    },
    retry: false,
    staleTime: 300000,
    refetchOnWindowFocus: false,
  });
  const fieldsReady = orderServiceFields.every(
    (field) => !!verifiedAttribute(registry.data, field.unique_code),
  );
  const selected = orderServiceFields.every((field) =>
    mappings.some(
      (mapping) =>
        mapping.source === field.source &&
        mapping.target === "attribute" &&
        !!mapping.attribute_id,
    ),
  );
  const pendingUnknown = orderServiceFields.some(
    (field) =>
      !verifiedAttribute(registry.data, field.unique_code) &&
      intents[field.unique_code]?.state === "unknown",
  );
  const unattempted = orderServiceFields.filter(
    (field) =>
      !verifiedAttribute(registry.data, field.unique_code) &&
      !intents[field.unique_code],
  );
  function persist(next: Intents) {
    try {
      sessionStorage.setItem(storageKey(scope), JSON.stringify(next));
      setIntents(next);
      return true;
    } catch {
      setStorageError(
        "Не удалось сохранить подтверждение в этой вкладке. Разрешите хранение данных сайта перед созданием полей.",
      );
      return false;
    }
  }
  async function createFields() {
    if (
      lock.current ||
      !available ||
      disabled ||
      !consent ||
      !registry.data ||
      !unattempted.length ||
      pendingUnknown
    )
      return;
    lock.current = true;
    setBusy(true);
    onBusy(true);
    setStorageError("");
    let current = { ...intents };
    try {
      for (const field of unattempted) {
        const intent: Intent = { key: crypto.randomUUID(), state: "unknown" };
        current = { ...current, [field.unique_code]: intent };
        if (!persist(current)) break;
        try {
          const result = await post<SetupResult>(
            "/api/orders/metadata/attributes",
            {
              ...orderScopeBody(scope),
              unique_code: field.unique_code,
              type: "string",
              idempotency_key: intent.key,
              acknowledge: true,
            },
            AbortSignal.timeout(30000),
          );
          if (result.registry && !sameOrderScope(result.registry.scope, scope))
            throw new Error("registry scope mismatch");
          if (result.registry)
            client.setQueryData(registryKey, result.registry);
          const ready =
            result.state === "RECONCILED" &&
            !!verifiedAttribute(result.registry, field.unique_code);
          const knownFailure = ["REJECTED", "BLOCKED"].includes(result.state);
          const denied = result.code === "PROVIDER_ADMIN_REQUIRED";
          current = {
            ...current,
            [field.unique_code]: {
              ...intent,
              state: ready
                ? "reconciled"
                : denied
                  ? "denied"
                  : knownFailure
                    ? "failed"
                    : "unknown",
              message: denied
                ? "Недостаточно прав для создания поля в МойСклад. Нужны права администратора подключённого пользователя."
                : knownFailure
                  ? "Создание не подтверждено. Проверьте права и настройки МойСклад."
                  : undefined,
            },
          };
        } catch (error) {
          const denied = error instanceof ApiError && error.status === 403;
          const rejected =
            error instanceof ApiError &&
            [400, 403, 404, 409, 422].includes(error.status);
          current = {
            ...current,
            [field.unique_code]: {
              ...intent,
              state: denied ? "denied" : rejected ? "failed" : "unknown",
              message: denied
                ? "Недостаточно прав для создания поля в МойСклад. Проверьте права подключённого пользователя."
                : rejected
                  ? orderRequestError(error)
                  : undefined,
            },
          };
        }
        persist(current);
        if (current[field.unique_code].state === "unknown") break;
      }
    } finally {
      await client.invalidateQueries({ queryKey: registryKey });
      await client.invalidateQueries({ queryKey: ["orders-metadata", scope] });
      setConsent(false);
      lock.current = false;
      setBusy(false);
      onBusy(false);
    }
  }
  function toggleMappings(checked: boolean) {
    if (disabled || busy || (checked && !fieldsReady)) return;
    const sources = orderServiceFields.map((field) => String(field.source));
    const next = mappings.filter(
      (mapping) =>
        !(mapping.target === "attribute" && sources.includes(mapping.source)),
    );
    if (checked)
      for (const field of orderServiceFields)
        next.push({
          source: field.source,
          target: "attribute",
          attribute_id: verifiedAttribute(registry.data, field.unique_code)!
            .attribute_id,
        });
    onMappings(next);
  }
  return (
    <fieldset className="orders-service-fields">
      <legend>Служебные поля Ozon</legend>
      <p>
        В заказе МойСклад можно хранить ID заказа, статус и подстатус Ozon. Это
        сведения об обработке заказа.
      </p>
      <p className="orders-note">
        Аккаунт МойСклад: <strong>{targetLabel}</strong>. Создание полей не
        меняет существующие заказы.
      </p>
      {registry.isLoading && <p role="status">Проверяем служебные поля…</p>}
      {registry.isError && (
        <p role="alert" className="orders-error">
          Не удалось проверить сохранённые служебные поля.{" "}
          {orderRequestError(registry.error)}
        </p>
      )}
      <ul className="orders-service-list">
        {orderServiceFields.map((field) => {
          const ready = !!verifiedAttribute(registry.data, field.unique_code);
          const intent = intents[field.unique_code];
          return (
            <li key={field.unique_code}>
              <strong>{field.label}</strong>
              <span
                className={`orders-badge ${ready ? "success" : intent ? "warning" : ""}`}
              >
                {ready
                  ? "Подтверждено в МойСклад"
                  : intent?.state === "unknown"
                    ? busy
                      ? "Проверяем создание"
                      : "Результат неизвестен"
                    : intent?.state === "denied"
                      ? "Недостаточно прав"
                      : intent
                        ? "Создание не подтверждено"
                        : "Не настроено"}
              </span>
              {!ready && intent?.message && (
                <p className="orders-warning">{intent.message}</p>
              )}
            </li>
          );
        })}
      </ul>
      {pendingUnknown && !busy && (
        <p className="orders-warning" role="alert">
          Результат создания неизвестен: поле могло появиться в МойСклад.
          Повторная отправка не выполняется. Обновите сведения о полях для
          проверки результата.
        </p>
      )}
      {storageError && (
        <p role="alert" className="orders-error">
          {storageError}
        </p>
      )}
      {available && !fieldsReady && (
        <>
          <label className="orders-check">
            <input
              type="checkbox"
              checked={consent}
              disabled={
                busy ||
                disabled ||
                pendingUnknown ||
                !unattempted.length ||
                !registry.data
              }
              onChange={(event) => setConsent(event.target.checked)}
            />
            Разрешаю создать отсутствующие служебные поля Ozon в аккаунте
            МойСклад «{targetLabel}».
          </label>
          <button
            type="button"
            className="secondary"
            disabled={
              disabled ||
              busy ||
              !consent ||
              pendingUnknown ||
              !unattempted.length ||
              !registry.data
            }
            onClick={() => void createFields()}
          >
            {busy
              ? "Создаём и проверяем служебные поля…"
              : "Создать служебные поля Ozon в МойСклад"}
          </button>
        </>
      )}
      <button
        type="button"
        className="text-action"
        disabled={busy || registry.isFetching}
        onClick={() => void registry.refetch()}
      >
        Обновить сведения о служебных полях
      </button>
      <label className="orders-check">
        <input
          type="checkbox"
          checked={selected}
          disabled={disabled || busy || (!selected && !fieldsReady)}
          onChange={(event) => toggleMappings(event.target.checked)}
        />
        Переносить ID и статусы в служебные поля
      </label>
      <p className="orders-note">
        Передача включится только после сохранения настроек. Создание полей не
        подтверждает передачу заказов.
      </p>
    </fieldset>
  );
}
