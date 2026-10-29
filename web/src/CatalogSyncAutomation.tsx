import {
  fieldLabel,
  providerLabel,
  type Authority,
  type CatalogConfig,
  type Enrollment,
} from "./CatalogSyncTypes";

export function needsAutomaticConsent(
  config: CatalogConfig,
  targets: Enrollment[],
  policies: Record<string, Authority>,
  enabled: boolean,
) {
  return targets.some((target) => {
    if (!target.automatic) return false;
    const previous = config.targets.find((item) => item.id === target.id);
    return (
      (!config.enabled && enabled) ||
      !previous?.automatic ||
      (target.automatic_fields || []).some(
        (field) =>
          !previous.automatic_fields?.includes(field) ||
          config.rules[field] !== policies[field],
      )
    );
  });
}

export function CatalogSyncAutomation({
  config,
  targets,
  policies,
  enabled,
  busy,
  onChange,
}: {
  config: CatalogConfig;
  targets: Enrollment[];
  policies: Record<string, Authority>;
  enabled: boolean;
  busy: boolean;
  onChange: (targets: Enrollment[]) => void;
}) {
  const available = enabled && config.publication_available === true;
  const fields = ["name", "description"].filter(
    (field) =>
      config.fields?.some((item) => item.field === field && item.writable) &&
      ["MOYSKLAD_AUTHORITATIVE", "OZON_AUTHORITATIVE"].includes(
        policies[field],
      ),
  );
  function change(target: Enrollment, patch: Partial<Enrollment>) {
    onChange(
      targets.map((item) =>
        item.id === target.id ? { ...item, ...patch } : item,
      ),
    );
  }
  return (
    <details className="catalog-facts">
      <summary>Автоматическая передача для связанных товаров</summary>
      <div className="catalog-facts-body">
        <p>
          После сохранения разрешённые изменения передаются без отдельного
          подтверждения. Конфликты и неизвестный результат требуют проверки.
          Создание товара всегда подтверждается отдельно.
        </p>
        {!available && (
          <p>
            Для включения нужны разрешение аккаунта и включённая синхронизация.
            Ранее разрешённую автоматизацию можно отключить.
          </p>
        )}
        {!targets.length && (
          <p>
            Сначала выберите товары, сохраните выбор и проверьте соответствия.
          </p>
        )}
        {targets.map((target) => {
          const label = target.source.offer_id || target.source.id;
          const existing = Boolean(
            target.mapping_id && target.target.id && !target.create,
          );
          return (
            <fieldset className="catalog-automatic-target" key={target.id}>
              <legend>
                {label} · {providerLabel(target.source.account.provider)} ↔{" "}
                {providerLabel(target.target.account.provider)}
              </legend>
              {!existing ? (
                <p>
                  Создание и неподтверждённые соответствия доступны только с
                  отдельной проверкой и подтверждением.
                </p>
              ) : (
                <>
                  <label className="catalog-check">
                    <input
                      type="checkbox"
                      checked={target.automatic}
                      disabled={
                        busy ||
                        (!target.automatic && (!available || !fields.length))
                      }
                      onChange={(event) =>
                        change(target, { automatic: event.target.checked })
                      }
                    />
                    Автоматически передавать изменения товара {label}
                  </label>
                  <div className="catalog-automatic-fields">
                    {[
                      ...new Set([
                        ...fields,
                        ...(target.automatic_fields || []),
                      ]),
                    ].map((field) => {
                      const checked = Boolean(
                        target.automatic_fields?.includes(field),
                      );
                      return (
                        <label className="catalog-check" key={field}>
                          <input
                            type="checkbox"
                            checked={checked}
                            disabled={
                              busy ||
                              !target.automatic ||
                              (!checked &&
                                (!available || !fields.includes(field)))
                            }
                            onChange={(event) =>
                              change(target, {
                                automatic_fields: event.target.checked
                                  ? [...(target.automatic_fields || []), field]
                                  : (target.automatic_fields || []).filter(
                                      (item) => item !== field,
                                    ),
                              })
                            }
                          />
                          {fieldLabel(field)}
                          {policies[field] === "MOYSKLAD_AUTHORITATIVE"
                            ? " · из МойСклад"
                            : policies[field] === "OZON_AUTHORITATIVE"
                              ? " · из Ozon"
                              : " · проверьте источник поля"}
                        </label>
                      );
                    })}
                  </div>
                  {!fields.length && (
                    <small>
                      Выберите поддерживаемый источник названия или описания.
                      Доступность полей определяет сервер.
                    </small>
                  )}
                </>
              )}
            </fieldset>
          );
        })}
      </div>
    </details>
  );
}
