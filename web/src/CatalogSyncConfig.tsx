import { useRef, useState, type FormEvent } from "react";
import { Link } from "react-router-dom";
import { BrandLogo } from "./BrandLogo";
import { Icon } from "./UI";
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
  automationOnly = false,
}: {
  scope: Scope;
  config: CatalogConfig;
  onSaved: (config: CatalogConfig) => void;
  onDirty: (dirty: boolean) => void;
  automationOnly?: boolean;
}) {
  const [preset, setPreset] = useState<string | null>(null);
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
    setPreset("fields");
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
    setPreset(authority === "MOYSKLAD_AUTHORITATIVE" ? "moy" : "ozon");
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
  function reset() {
    setPreset(null);
    setPolicies(config.rules);
    setEnabled(config.enabled);
    setTargets(config.targets);
    setInterval(config.interval_seconds);
    setAutomaticConsent(false);
    onDirty(false);
  }
  const mode =
    preset ||
    (fields
      .filter(visibleField)
      .every((f) => policies[f] === "MOYSKLAD_AUTHORITATIVE")
      ? "moy"
      : fields
            .filter(visibleField)
            .every((f) => policies[f] === "OZON_AUTHORITATIVE")
        ? "ozon"
        : "fields");
  function renderPolicy(field: string) {
    const authority = policies[field] || "IGNORE";
    const destination =
      authority === "MOYSKLAD_AUTHORITATIVE"
        ? "ozon"
        : authority === "OZON_AUTHORITATIVE"
          ? "moysklad"
          : null;
    return (
      <tr key={field}>
        <th scope="row">
          <span className="products-field-icon">
            {field === "name" ? "T" : "≡"}
          </span>
          {fieldLabel(field)}
        </th>
        <td>
          <label className="products-authority">
            <span className="products-sr">Источник: {fieldLabel(field)}</span>
            {destination && (
              <BrandLogo
                provider={destination === "ozon" ? "moysklad" : "ozon"}
                decorative
              />
            )}
            <select
              id={`catalog-policy-${field}`}
              aria-label={fieldLabel(field)}
              value={authority}
              disabled={busy || !visibleField(field)}
              onChange={(e) =>
                change({ ...policies, [field]: e.target.value as Authority })
              }
            >
              {authorities.map((a) => (
                <option key={a.value} value={a.value}>
                  {a.label}
                </option>
              ))}
            </select>
          </label>
        </td>
        <td>
          <span className="products-direction-arrow">→</span>
        </td>
        <td>
          {destination ? (
            <span className="products-destination">
              <BrandLogo provider={destination} decorative />
              {destination === "ozon" ? "Ozon" : "МойСклад"}
            </span>
          ) : (
            <span className="products-muted">
              {authority === "IGNORE" ? "Не передавать" : "По выбору оператора"}
            </span>
          )}
        </td>
        <td>
          <span className={`mapping-chip ${destination ? "ok" : ""}`}>
            {destination
              ? "После подтверждения"
              : authority === "MANUAL"
                ? "Вручную"
                : "Выключено"}
          </span>
        </td>
      </tr>
    );
  }
  const saveControls = (
    <>
      {needsConsent && (
        <label className="catalog-check catalog-warning">
          <input
            type="checkbox"
            checked={automaticConsent}
            disabled={busy}
            onChange={(e) => setAutomaticConsent(e.target.checked)}
          />
          Разрешаю автоматическую передачу выбранных полей указанных товаров без
          отдельного подтверждения каждой отправки
        </label>
      )}
      {automaticInvalid && (
        <p className="catalog-error" role="alert">
          Выберите хотя бы одно поле для каждого товара с автоматической
          передачей.
        </p>
      )}
      <div className="products-save-bar">
        <span role="status">
          <Icon name={dirty ? "diagnostics" : "check"} size="button" />
          {dirty ? "Есть несохранённые изменения" : "Изменения сохранены"}
        </span>
        <button
          type="button"
          className="secondary"
          disabled={busy || !dirty}
          onClick={reset}
        >
          Отменить
        </button>
        <button
          disabled={
            busy ||
            !dirty ||
            automaticInvalid ||
            (needsConsent && !automaticConsent)
          }
        >
          {busy
            ? "Сохраняем…"
            : automationOnly
              ? "Сохранить разрешения"
              : "Сохранить правила"}
        </button>
      </div>
      {error && (
        <p className="catalog-error" role="alert">
          {error}
        </p>
      )}
    </>
  );
  return (
    <section
      className={`catalog-section products-rules ${automationOnly ? "products-automation-page" : ""}`}
      aria-labelledby="catalog-config-title"
    >
      <form onSubmit={(event) => void save(event)}>
        {!automationOnly && (
          <>
            <h2 id="catalog-config-title">Откуда брать данные о товарах</h2>
            <p>Выберите, какие значения считать правильными в каждом сервисе</p>
            <div className="products-rules-enable">
              <label className="products-switch">
                <input
                  type="checkbox"
                  aria-label="Разрешить синхронизацию каталога для выбранных аккаунтов"
                  checked={enabled}
                  disabled={busy}
                  onChange={(e) => {
                    setAutomaticConsent(false);
                    setEnabled(e.target.checked);
                    onDirty(isDirty(policies, e.target.checked));
                  }}
                />
                <span aria-hidden="true" />
                <b>Синхронизация товаров</b>
              </label>
              <label className="products-interval">
                <span className="products-sr">
                  Интервал автоматических проверок, секунд
                </span>
                <select
                  aria-label="Интервал автоматических проверок, секунд"
                  value={interval}
                  disabled={busy}
                  onChange={(e) => {
                    const n = Number(e.target.value);
                    setInterval(n);
                    onDirty(isDirty(policies, enabled, n));
                  }}
                >
                  {[...new Set([60, 300, 900, 1800, 3600, interval])]
                    .sort((a, b) => a - b)
                    .map((n) => (
                      <option key={n} value={n}>
                        Проверять каждые{" "}
                        {n < 60 ? `${n} секунд` : `${n / 60} минут`}
                      </option>
                    ))}
                </select>
              </label>
            </div>
            <div
              className="products-rule-presets"
              aria-label="Направление синхронизации"
            >
              <button
                type="button"
                className={mode === "moy" ? "active" : "secondary"}
                disabled={busy}
                onClick={() => direction("MOYSKLAD_AUTHORITATIVE")}
              >
                Из МойСклад
              </button>
              <button
                type="button"
                className={mode === "ozon" ? "active" : "secondary"}
                disabled={busy}
                onClick={() => direction("OZON_AUTHORITATIVE")}
              >
                Из Ozon
              </button>
              <button
                type="button"
                className={mode === "fields" ? "active" : "secondary"}
                disabled={busy}
                onClick={() => setPreset("fields")}
              >
                По полям
              </button>
            </div>
            <div className="products-rules-table products-policy-table">
              <table>
                <thead>
                  <tr>
                    <th>Поле товара</th>
                    <th>Источник правильных данных</th>
                    <th />
                    <th>Куда передавать</th>
                    <th>Режим</th>
                  </tr>
                </thead>
                <tbody>{fields.filter(visibleField).map(renderPolicy)}</tbody>
              </table>
            </div>
            <details className="products-field-limits">
              <summary>
                Другие поля и ограничения{" "}
                <span>Настроены на уровне системы</span>
              </summary>
              <div className="products-limits-list">
                {fields
                  .filter((f) => !visibleField(f))
                  .map((f) => (
                    <p key={f}>
                      <b>{fieldLabel(f)}</b>
                      <select
                        aria-label={fieldLabel(f)}
                        value={policies[f] || "IGNORE"}
                        disabled
                      >
                        <option value={policies[f] || "IGNORE"}>
                          Передача пока не поддерживается
                        </option>
                      </select>
                    </p>
                  ))}
              </div>
            </details>
            <div className="products-rules-bottom">
              <section>
                <header>
                  <h3>Автоматизация</h3>
                  <span className="mapping-chip ok">
                    {targets.filter((t) => t.automatic).length} товаров
                    настроено
                  </span>
                </header>
                <p>
                  Синхронизация будет применяться только к выбранным товарам.
                  Можно добавить или изменить список в любой момент.
                </p>
                <Link className="secondary" to="/products/rules/automation">
                  Выбрать товары <Icon name="arrow" size="button" />
                </Link>
              </section>
              <section>
                <h3>Как это работает</h3>
                <ol>
                  <li>
                    Выберите, в каком сервисе значения считать правильными для
                    каждого поля.
                  </li>
                  <li>
                    После подтверждения данные передаются в выбранный сервис.
                  </li>
                  <li>
                    Разрешённые поля выбранных товаров проверяются по
                    расписанию.
                  </li>
                </ol>
                <p className="products-inline-note">
                  Смена правил сама не отправляет товары.
                </p>
              </section>
            </div>
          </>
        )}
        {automationOnly && (
          <CatalogSyncAutomation
            scope={scope}
            config={config}
            targets={targets}
            policies={policies}
            enabled={enabled}
            busy={busy}
            actions={saveControls}
            onChange={(next) => {
              setAutomaticConsent(false);
              setTargets(next);
              onDirty(isDirty(policies, enabled, interval, next));
            }}
          />
        )}
        {!automationOnly && saveControls}
      </form>
    </section>
  );
}
