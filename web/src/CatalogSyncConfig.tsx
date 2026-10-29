import { useRef, useState, type FormEvent } from "react";
import { post } from "./api";
import {
  fieldLabel,
  fieldLabels,
  configBody,
  normalizeConfig,
  requestError,
  scopeBody,
  type Authority,
  type CatalogConfig,
  type Scope,
} from "./CatalogSyncTypes";

const authorities: { value: Authority; label: string }[] = [
  { value: "MOYSKLAD_AUTHORITATIVE", label: "МойСклад" },
  { value: "OZON_AUTHORITATIVE", label: "Ozon" },
  { value: "MANUAL", label: "Вручную" },
  { value: "IGNORE", label: "Не передавать" },
];

export function CatalogSyncConfig({
  scope,
  config,
  onSaved,
  onDirty,
}: {
  scope: Scope;
  config: CatalogConfig;
  onSaved: (config: CatalogConfig) => void;
  onDirty: (dirty: boolean) => void;
}) {
  const [policies, setPolicies] = useState(config.rules);
  const [enabled, setEnabled] = useState(config.enabled);
  const [interval, setInterval] = useState(config.interval_seconds);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const lock = useRef(false);
  const fields = [
    ...new Set([
      ...Object.keys(fieldLabels),
      ...Object.keys(config.rules),
      ...(config.fields || []).map((f) => f.field),
    ]),
  ];
  const dirty =
    JSON.stringify(policies) !== JSON.stringify(config.rules) ||
    enabled !== config.enabled ||
    interval !== config.interval_seconds;
  function change(next: Record<string, Authority>) {
    setPolicies(next);
    onDirty(
      JSON.stringify(next) !== JSON.stringify(config.rules) ||
        enabled !== config.enabled ||
        interval !== config.interval_seconds,
    );
  }
  async function save(event: FormEvent) {
    event.preventDefault();
    if (lock.current || !dirty) return;
    if (!Number.isInteger(interval) || interval < 30 || interval > 86400) {
      setError("Укажите целый интервал от 30 до 86400 секунд.");
      return;
    }
    lock.current = true;
    setBusy(true);
    setError("");
    try {
      const value = await post<CatalogConfig>(
        "/api/catalog-sync/config",
        {
          ...scopeBody(scope),
          config: configBody({
            ...config,
            scope: scopeBody(scope),
            rules: policies,
            enabled,
            interval_seconds: interval,
          }),
          expected_revision: config.revision,
          idempotency_key: crypto.randomUUID(),
        },
        AbortSignal.timeout(30000),
      );
      onSaved(normalizeConfig(value));
    } catch (error) {
      setError(
        requestError(error) +
          " Обновите настройки, если результат сохранения неизвестен.",
      );
    } finally {
      lock.current = false;
      setBusy(false);
    }
  }
  return (
    <section className="catalog-section" aria-labelledby="catalog-config-title">
      <h2 id="catalog-config-title">Источник данных для каждого поля</h2>
      <p>
        Изменение в выбранном источнике передаётся во второй сервис после
        проверки и подтверждения.
      </p>
      <form onSubmit={(event) => void save(event)}>
        <label className="catalog-check">
          <input
            type="checkbox"
            checked={enabled}
            disabled={busy}
            onChange={(event) => {
              setEnabled(event.target.checked);
              onDirty(
                JSON.stringify(policies) !== JSON.stringify(config.rules) ||
                  event.target.checked !== config.enabled ||
                  interval !== config.interval_seconds,
              );
            }}
          />
          Разрешить синхронизацию каталога для выбранных аккаунтов
        </label>
        <p>
          Настройка не подтверждает изменения товаров. Возможность отправки
          зависит от подключения и разрешений аккаунта.
        </p>
        {config.targets.some((target) => target.automatic) && (
          <div className="catalog-warning">
            <p>
              Для ранее разрешённых товаров включён автоматический режим. Он
              передаёт только разрешённые поля.
            </p>
            <ul>
              {config.targets
                .filter((target) => target.automatic)
                .map((target) => (
                  <li key={target.id}>
                    {target.source.offer_id || target.source.id}:{" "}
                    {(target.automatic_fields || []).map(fieldLabel).join(", ")}
                  </li>
                ))}
            </ul>
          </div>
        )}
        <label>
          Интервал автоматических проверок, секунд
          <input
            type="number"
            min={30}
            max={86400}
            step={1}
            required
            value={Number.isNaN(interval) ? "" : interval}
            disabled={busy}
            onChange={(event) => {
              const next = event.target.valueAsNumber;
              setInterval(next);
              onDirty(
                JSON.stringify(policies) !== JSON.stringify(config.rules) ||
                  enabled !== config.enabled ||
                  next !== config.interval_seconds,
              );
            }}
          />
          <small>
            От 30 до 86400 секунд. Новые выбранные товары добавляются без
            автоматической отправки.
          </small>
        </label>
        <div className="catalog-presets" aria-label="Направление синхронизации">
          <button
            type="button"
            className="secondary"
            disabled={busy}
            onClick={() =>
              change(
                Object.fromEntries(
                  fields.map((f) => [f, "MOYSKLAD_AUTHORITATIVE"]),
                ),
              )
            }
          >
            МойСклад → Ozon
          </button>
          <button
            type="button"
            className="secondary"
            disabled={busy}
            onClick={() =>
              change(
                Object.fromEntries(
                  fields.map((f) => [f, "OZON_AUTHORITATIVE"]),
                ),
              )
            }
          >
            Ozon → МойСклад
          </button>
          <span>Для смешанного направления выберите источники по полям.</span>
        </div>
        <div className="catalog-policies">
          {fields.map((field) => {
            const capability = config.fields?.find(
              (item) => item.field === field,
            );
            return (
              <div className="catalog-policy" key={field}>
                <label htmlFor={`catalog-policy-${field}`}>
                  {fieldLabel(field)}
                </label>
                <select
                  id={`catalog-policy-${field}`}
                  value={policies[field] || "IGNORE"}
                  disabled={busy}
                  onChange={(event) =>
                    change({
                      ...policies,
                      [field]: event.target.value as Authority,
                    })
                  }
                >
                  {authorities.map((authority) => (
                    <option key={authority.value} value={authority.value}>
                      {authority.label}
                    </option>
                  ))}
                </select>
                {capability && !capability.writable && (
                  <small>
                    Передача пока не поддерживается. Проверка покажет пропуск.
                  </small>
                )}
                {policies[field] === "MANUAL" && (
                  <small>
                    Фактическое значение нужно указать для выбранного товара.
                  </small>
                )}
              </div>
            );
          })}
        </div>
        <div className="catalog-actions">
          <button disabled={busy || !dirty}>
            {busy ? "Сохраняем…" : "Сохранить настройки"}
          </button>
          <span role="status">
            {dirty
              ? "Есть несохранённые настройки. Сначала сохраните их."
              : "Настройки сохранены"}
          </span>
        </div>
        {error && (
          <p className="catalog-error" role="alert">
            {error}
          </p>
        )}
      </form>
    </section>
  );
}
