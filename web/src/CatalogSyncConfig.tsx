import { useRef, useState, type FormEvent } from "react";
import { post } from "./api";
import {
  CatalogSyncAutomation,
  needsAutomaticConsent,
} from "./CatalogSyncAutomation";
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
  const [targets, setTargets] = useState(config.targets);
  const [automaticConsent, setAutomaticConsent] = useState(false);
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
  function isDirty(
    next = policies,
    permission = enabled,
    seconds = interval,
    products = targets,
  ) {
    return (
      JSON.stringify(next) !== JSON.stringify(config.rules) ||
      JSON.stringify(products) !== JSON.stringify(config.targets) ||
      permission !== config.enabled ||
      seconds !== config.interval_seconds ||
      (permission && config.publication_available === true) !==
        config.test_enabled
    );
  }
  const dirty = isDirty();
  const needsConsent = needsAutomaticConsent(
    config,
    targets,
    policies,
    enabled,
  );
  const automaticInvalid = targets.some(
    (target) => target.automatic && !target.automatic_fields?.length,
  );
  function change(next: Record<string, Authority>) {
    setAutomaticConsent(false);
    setPolicies(next);
    onDirty(isDirty(next));
  }
  function direction(authority: Authority) {
    const writable = config.fields
      ? config.fields
          .filter((field) => field.writable)
          .map((field) => field.field)
      : Object.entries(config.rules)
          .filter(([, value]) => value !== "IGNORE")
          .map(([field]) => field);
    change({
      ...policies,
      ...Object.fromEntries(writable.map((field) => [field, authority])),
    });
  }
  async function save(event: FormEvent) {
    event.preventDefault();
    if (
      lock.current ||
      !dirty ||
      automaticInvalid ||
      (needsConsent && !automaticConsent)
    )
      return;
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
            targets,
            enabled,
            test_enabled: enabled && config.publication_available === true,
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
  function visibleField(field: string) {
    const capability = config.fields?.find((item) => item.field === field);
    return capability
      ? capability.writable
      : ["name", "description"].includes(field);
  }
  function renderPolicy(field: string) {
    const capability = config.fields?.find((item) => item.field === field);
    return (
      <div className="catalog-policy" key={field}>
        <label htmlFor={`catalog-policy-${field}`}>{fieldLabel(field)}</label>
        <select
          id={`catalog-policy-${field}`}
          value={policies[field] || "IGNORE"}
          disabled={busy}
          onChange={(event) =>
            change({ ...policies, [field]: event.target.value as Authority })
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
              setAutomaticConsent(false);
              setEnabled(event.target.checked);
              onDirty(isDirty(policies, event.target.checked));
            }}
          />
          Разрешить синхронизацию каталога для выбранных аккаунтов
        </label>
        <p>
          Настройка не подтверждает изменения товаров. Возможность отправки
          зависит от подключения и разрешений аккаунта.
        </p>
        <CatalogSyncAutomation
          config={config}
          targets={targets}
          policies={policies}
          enabled={enabled}
          busy={busy}
          onChange={(next) => {
            setAutomaticConsent(false);
            setTargets(next);
            onDirty(isDirty(policies, enabled, interval, next));
          }}
        />
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
              onDirty(isDirty(policies, enabled, next));
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
            onClick={() => direction("MOYSKLAD_AUTHORITATIVE")}
          >
            МойСклад → Ozon
          </button>
          <button
            type="button"
            className="secondary"
            disabled={busy}
            onClick={() => direction("OZON_AUTHORITATIVE")}
          >
            Ozon → МойСклад
          </button>
          <span>Для смешанного направления выберите источники по полям.</span>
        </div>
        <div className="catalog-policies">
          {fields.filter((field) => visibleField(field)).map(renderPolicy)}
        </div>
        <details className="catalog-facts">
          <summary>Другие поля и ограничения</summary>
          <div className="catalog-policies">
            {fields.filter((field) => !visibleField(field)).map(renderPolicy)}
          </div>
        </details>
        {needsConsent && (
          <label className="catalog-check catalog-warning">
            <input
              type="checkbox"
              checked={automaticConsent}
              disabled={busy}
              onChange={(event) => setAutomaticConsent(event.target.checked)}
            />
            Разрешаю автоматическую передачу выбранных полей указанных товаров
            без отдельного подтверждения каждой отправки
          </label>
        )}
        {automaticInvalid && (
          <p className="catalog-error" role="alert">
            Выберите хотя бы одно поле для каждого товара с автоматической
            передачей.
          </p>
        )}
        <div className="catalog-actions">
          <button
            disabled={
              busy ||
              !dirty ||
              automaticInvalid ||
              (needsConsent && !automaticConsent)
            }
          >
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
