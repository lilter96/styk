import { useState, type ReactNode } from "react";
import { NavLink } from "react-router-dom";
import type { Batch, Report } from "./api";
import { Icon, type IconName } from "./UI";
import "./overview.css";

const format = (value: number | undefined) =>
  value === undefined ? "Нет данных" : value.toLocaleString("ru-RU");
const count = (batch: Batch | undefined, key: string) =>
  batch?.present ? batch.counts?.[key] : undefined;
const metrics = [
  ["products", "Товары"],
  ["warehouses", "Склады"],
  ["stock_rows", "Записи остатков"],
] as const;

function Metric({
  title,
  value,
  caption,
  icon,
  tone,
}: {
  title: string;
  value: number | undefined;
  caption: string;
  icon: IconName;
  tone: string;
}) {
  return (
    <article className={`overview-metric ${tone}`}>
      <span className="overview-metric-icon">
        <Icon name={icon} />
      </span>
      <h2>{title}</h2>
      <strong>{format(value)}</strong>
      <p>{caption}</p>
    </article>
  );
}

function CatalogChart({ report }: { report: Report }) {
  const [metric, setMetric] = useState<(typeof metrics)[number][0]>("products");
  const sources = [
    {
      name: "МойСклад",
      value: count(report.batches.moysklad, metric),
      provider: "moysklad",
    },
    {
      name: "Ozon",
      value: count(report.batches.ozon, metric),
      provider: "ozon",
    },
  ];
  const max = Math.max(1, ...sources.map((source) => source.value ?? 0));
  return (
    <section
      className="overview-card overview-catalog"
      aria-labelledby="overview-catalog-title"
    >
      <div className="overview-card-heading">
        <div>
          <h2 id="overview-catalog-title">Данные по сервисам</h2>
          <p>Последние загруженные каталоги</p>
        </div>
        <span className="overview-tag">Текущий срез</span>
      </div>
      <div
        className="overview-chart-tabs"
        role="group"
        aria-label="Показатель графика"
      >
        {metrics.map(([key, label]) => (
          <button
            key={key}
            aria-pressed={metric === key}
            onClick={() => setMetric(key)}
          >
            {label}
          </button>
        ))}
      </div>
      <div className="overview-bars">
        {sources.map(({ name, value, provider }) => (
          <div className="overview-bar-row" key={provider}>
            <div>
              <span>{name}</span>
              <b>{format(value)}</b>
            </div>
            <div
              className="overview-bar-track"
              role="img"
              aria-label={`${name}: ${format(value)}`}
            >
              {value !== undefined && (
                <span
                  className={provider}
                  style={{ width: `${(value / max) * 100}%` }}
                />
              )}
            </div>
          </div>
        ))}
      </div>
      <p className="overview-footnote">
        {metric === "stock_rows"
          ? "Число записей, а не количество единиц товара. Отсутствующая запись не означает нулевой остаток."
          : "Сравнение загруженных данных, без объединения одинаковых товаров и складов двух сервисов."}
      </p>
    </section>
  );
}

function MappingChart({ report }: { report: Report }) {
  const total = report.mapping_counts.catalog_products;
  const confirmed = report.mapping_counts.catalog_confirmed_products;
  const available =
    total !== undefined &&
    confirmed !== undefined &&
    total >= confirmed &&
    confirmed >= 0;
  const percent = available && total > 0 ? (confirmed / total) * 100 : 0;
  return (
    <section
      className="overview-card overview-mapping"
      aria-labelledby="overview-mapping-title"
    >
      <div className="overview-card-heading">
        <div>
          <h2 id="overview-mapping-title">Сопоставление товаров</h2>
          <p>Текущий каталог Ozon</p>
        </div>
      </div>
      <div
        className="overview-donut"
        role="img"
        aria-label={
          available
            ? `Подтверждено ${confirmed} из ${total} товаров`
            : "Нет данных о сопоставлении текущего каталога"
        }
        style={{
          background: `conic-gradient(var(--styk-primary) ${percent}%, var(--styk-line) 0)`,
        }}
      >
        <div>
          <strong>{available ? `${Math.round(percent)}%` : "—"}</strong>
          <span>{available ? "сопоставлено" : "нет данных"}</span>
        </div>
      </div>
      <div className="overview-distribution">
        <span>
          <i />
          Подтверждено
        </span>
        <b>{format(available ? confirmed : undefined)}</b>
      </div>
      <div className="overview-distribution">
        <span>
          <i />
          Ожидает сопоставления
        </span>
        <b>{format(available ? total - confirmed : undefined)}</b>
      </div>
      <NavLink className="overview-text-link" to="/products">
        Открыть товары <Icon name="arrow" />
      </NavLink>
    </section>
  );
}

function checkLink(code: string) {
  if (code === "PRODUCT_MAPPINGS" || code === "MATCHING_SUGGESTIONS")
    return "/products";
  if (code === "WAREHOUSE_MAPPINGS") return "/warehouses";
  if (
    code.endsWith("_CONNECTION") ||
    code.endsWith("_IMPORT") ||
    code.endsWith("_REGISTRATION")
  )
    return "/integrations";
  return "/diagnostics";
}

export function OverviewDashboard({
  report,
  children,
  checkTitles,
  checkSummary,
}: {
  report: Report;
  children: ReactNode;
  checkTitles: Record<string, string>;
  checkSummary: (code: string, status: string) => string;
}) {
  const issues = report.checks.filter((check) => check.status !== "PASS");
  const failed = report.checks.filter(
    (check) => check.status === "FAIL",
  ).length;
  return (
    <div className="read-page overview-page">
      <section className="overview-welcome">
        <div>
          <h1>Добро пожаловать в STYK</h1>
          <p>Ваши товары, склады и подключения — в одном месте.</p>
          <NavLink to="/products">
            Посмотреть товары <Icon name="arrow" />
          </NavLink>
          <span className="overview-welcome-note">
            Данные выбранных аккаунтов
          </span>
        </div>
        <div className="overview-hero-art" aria-hidden="true">
          <span />
          <span />
          <span />
          <b>✦</b>
        </div>
      </section>
      <div className="overview-section-heading">
        <div>
          <h2>Обзор данных</h2>
          <p>Последние загрузки и состояние сопоставлений</p>
        </div>
        <NavLink className="overview-text-link" to="/integrations">
          Все подключения <Icon name="arrow" />
        </NavLink>
      </div>
      <div className="overview-metrics">
        <Metric
          title="Товары Ozon"
          value={count(report.batches.ozon, "products")}
          caption="В последнем загруженном каталоге"
          icon="products"
          tone="violet"
        />
        <Metric
          title="Товары МойСклад"
          value={count(report.batches.moysklad, "products")}
          caption="Без учёта вариантов товара"
          icon="catalog"
          tone="blue"
        />
        <Metric
          title="Связи складов"
          value={report.mapping_counts.confirmed_warehouses}
          caption="Подтверждённые соответствия"
          icon="warehouse"
          tone="orange"
        />
        <Metric
          title="Ошибки проверок"
          value={report.checks.length ? failed : undefined}
          caption={`${issues.length} проверок требуют внимания`}
          icon="diagnostics"
          tone="red"
        />
      </div>
      <div className="overview-chart-grid">
        <CatalogChart report={report} />
        <MappingChart report={report} />
      </div>
      <div className="overview-bottom-grid">
        <div className="overview-sources">
          <div className="overview-section-heading">
            <div>
              <h2>Последние загрузки</h2>
              <p>Состояние данных в подключённых сервисах</p>
            </div>
          </div>
          {children}
        </div>
        <section
          className="overview-card overview-attention"
          aria-labelledby="overview-attention-title"
        >
          <div className="overview-card-heading">
            <div>
              <h2 id="overview-attention-title">Требуют внимания</h2>
              <p>Проверки и подключения</p>
            </div>
            <span className="overview-issue-count">{issues.length}</span>
          </div>
          <div className="overview-issues">
            {issues.map((check) => (
              <NavLink
                to={checkLink(check.code)}
                key={check.code}
                className="overview-issue"
              >
                <span
                  className={`overview-issue-icon ${check.status === "FAIL" ? "red" : "orange"}`}
                >
                  <Icon name="diagnostics" />
                </span>
                <span>
                  <b>{checkTitles[check.code] || "Дополнительная проверка"}</b>
                  <small>{checkSummary(check.code, check.status)}</small>
                </span>
                <Icon name="arrow" />
              </NavLink>
            ))}
          </div>
          {!issues.length && (
            <p className="overview-footnote">
              {report.checks.length
                ? "Все выполненные проверки пройдены."
                : "Проверки ещё не выполнялись."}
            </p>
          )}
          <NavLink className="overview-text-link" to="/diagnostics">
            Открыть диагностику <Icon name="arrow" />
          </NavLink>
        </section>
      </div>
    </div>
  );
}
