import type { NativeDocumentPolicy } from "./OrdersNativeTypes";
import { orderDate } from "./OrdersTypes";

export function nativePolicyValid(value: NativeDocumentPolicy | undefined) {
  if (!value?.enabled) return true;
  if (
    !/^\d+(?:\.\d+)?$/.test(value.maximum_quantity) ||
    Number(value.maximum_quantity) <= 0 ||
    Number(value.maximum_quantity) > 1000 ||
    !Number.isInteger(value.event_max_age_seconds) ||
    value.event_max_age_seconds < 1 ||
    value.event_max_age_seconds > 86400 ||
    !value.actions.length
  )
    return false;
  if (!value.allow_posted) return true;
  const start = Date.parse(value.attested_at);
  const end = Date.parse(value.valid_until);
  return (
    Number.isFinite(start) &&
    end > Date.now() &&
    end > start &&
    end - start <= 86400000
  );
}
export function OrdersNativePolicy({
  value,
  disabled,
  onChange,
}: {
  value: NativeDocumentPolicy | undefined;
  disabled: boolean;
  onChange: (policy: NativeDocumentPolicy) => void;
}) {
  const policy: NativeDocumentPolicy = value ?? {
    enabled: false,
    allow_posted: false,
    actions: ["demand_create", "salesreturn_create"],
    maximum_quantity: "10",
    event_max_age_seconds: 3600,
    attested_at: "0001-01-01T00:00:00Z",
    valid_until: "0001-01-01T00:00:00Z",
  };
  const change = (next: Partial<NativeDocumentPolicy>) =>
    onChange({ ...policy, ...next });
  return (
    <details>
      <summary>Отгрузки и возвраты МойСклад</summary>
      <fieldset disabled={disabled}>
        <p className="orders-note">
          Это отдельные складские документы. Они не создают оплату, не вызывают
          курьера и не меняют статус Ozon. Проведение меняет учёт остатков; для
          него нужны фактическое событие и отдельное подтверждение.
        </p>
        <label className="orders-check">
          <input
            type="checkbox"
            checked={policy.enabled}
            onChange={(event) =>
              change({ enabled: event.target.checked, allow_posted: false })
            }
          />
          Разрешить подготовку складских документов
        </label>
        {policy.enabled && (
          <>
            {(
              [
                ["demand_create", "Отгрузка"],
                [
                  "salesreturn_create",
                  "Возврат покупателя по связанной отгрузке",
                ],
              ] as const
            ).map(([action, title]) => (
              <label className="orders-check" key={action}>
                <input
                  type="checkbox"
                  checked={policy.actions.includes(action)}
                  onChange={(event) =>
                    change({
                      actions: event.target.checked
                        ? [...policy.actions, action]
                        : policy.actions.filter((item) => item !== action),
                    })
                  }
                />
                {title}
              </label>
            ))}
            <label>
              Максимальное количество в складском документе
              <input
                type="text"
                inputMode="decimal"
                maxLength={24}
                value={policy.maximum_quantity}
                onChange={(event) =>
                  change({ maximum_quantity: event.target.value })
                }
              />
            </label>
            <label>
              Допустимый возраст подтверждённого события, секунд
              <input
                type="number"
                min={1}
                max={86400}
                value={policy.event_max_age_seconds}
                onChange={(event) =>
                  change({ event_max_age_seconds: Number(event.target.value) })
                }
              />
            </label>
            <label className="orders-check orders-warning">
              <input
                type="checkbox"
                checked={policy.allow_posted}
                onChange={(event) => {
                  const now = Date.now();
                  change({
                    allow_posted: event.target.checked,
                    ...(event.target.checked
                      ? {
                          attested_at: new Date(now).toISOString(),
                          valid_until: new Date(now + 3600000).toISOString(),
                        }
                      : {}),
                  });
                }}
              />
              На один час разрешаю отдельное ручное подтверждение проведения по
              реальным складским событиям.
            </label>
            {policy.allow_posted && (
              <p>
                Разрешение действует до {orderDate(policy.valid_until)}. Оно не
                подтверждает, что товар уже отправлен или возвращён. Для
                продления снимите и снова явно установите разрешение.
              </p>
            )}
            {!nativePolicyValid(policy) && (
              <p className="orders-warning">
                Проверьте лимит, действия и срок разрешения складских
                документов.
              </p>
            )}
          </>
        )}
      </fieldset>
    </details>
  );
}
