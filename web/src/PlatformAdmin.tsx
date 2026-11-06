import { Fragment, useState, type SelectHTMLAttributes } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useSearchParams } from "react-router-dom";
import { api, ApiError, post } from "./api";
import { BrandLogo } from "./BrandLogo";
import { Icon, type IconName } from "./UI";
import "./platform-admin.css";

import {
  marketplacePlanKey,
  marketplacePlanName,
  type MarketplacePlan,
  type MarketplacePlanLimits,
} from "./marketplacePlan";

type Counts = Record<string, number>;
type Overview = {
  companies: number;
  connections: number;
  success: number;
  errors: number;
  providers: Counts;
  issues: {
    id: string;
    name: string;
    provider: string;
    finished_at: string;
    reason: string;
  }[];
};
const statuses = {
  active: "Активен",
  unverified: "Почта не подтверждена",
  disabled: "Отключён",
};
type Users = {
  total: number;
  users: {
    id: string;
    name: string;
    email: string;
    status: keyof typeof statuses;
    connections: number;
    providers: Counts;
  }[];
};
const providerNames: Record<string, string> = {
  ozon: "Ozon",
  moysklad: "МойСклад",
};
const providerColors: Record<string, string> = {
  moysklad: "#436dff",
  ozon: "#f33483",
};
const reasons: Record<string, string> = {
  provider_error: "Ошибка на стороне интеграции",
  invalid_response: "Некорректный ответ провайдера",
  limit_reached: "Достигнут лимит импорта",
  configuration: "Подключение недоступно",
  storage_error: "Не удалось сохранить данные",
};
const providerName = (id: string) => providerNames[id] ?? id;
const providerEntries = (counts: Counts) =>
  Object.entries(counts).sort(([left], [right]) => left.localeCompare(right));
const number = new Intl.NumberFormat("ru-RU");
const dateTime = new Intl.DateTimeFormat("ru-RU", {
  dateStyle: "short",
  timeStyle: "short",
});
const metrics: {
  key: "companies" | "connections" | "success" | "errors";
  title: string;
  icon: IconName;
}[] = [
  { key: "companies", title: "Компании", icon: "users" },
  { key: "connections", title: "Подключения", icon: "store" },
  { key: "success", title: "Успешные импорты", icon: "refresh" },
  { key: "errors", title: "Ошибки импорта", icon: "warning" },
];

function AdminSelect({
  icon,
  children,
  ...props
}: SelectHTMLAttributes<HTMLSelectElement> & { icon?: IconName }) {
  return (
    <span className="admin-select" data-leading={icon || undefined}>
      {icon && (
        <Icon name={icon} size="button" className="admin-control-leading" />
      )}
      <select className="admin-control" {...props}>
        {children}
      </select>
      <Icon
        name="chevron-down"
        size="button"
        className="admin-control-chevron"
      />
    </span>
  );
}

const sections = {
  plans: {
    title: "Тарифы и доступ",
    description: "Лимиты аккаунтов маркетплейсов для компаний STYK",
  },
  overview: {
    title: "Обзор платформы",
    description: "Компании, подключения и импорт данных в STYK",
  },
  users: {
    title: "Пользователи",
    description: "Аккаунты компаний и вход в кабинет для поддержки",
  },
  integrations: {
    title: "Интеграции",
    description: "Подключения по платформам и последние ошибки импорта",
  },
};
type AdminSection = keyof typeof sections;
const adminNavigation: {
  label: string;
  icon: IconName;
  section?: AdminSection;
}[] = [
  { label: "Обзор", icon: "overview", section: "overview" },
  { label: "Пользователи", icon: "users", section: "users" },
  { label: "Тарифы и доступ", icon: "layers", section: "plans" },
  { label: "Интеграции", icon: "integrations", section: "integrations" },
  { label: "Фоновые задачи", icon: "calendar" },
  { label: "Логи и события", icon: "catalog" },
  { label: "Мониторинг", icon: "activity" },
  { label: "Настройки", icon: "settings" },
];

function AdminSidebar({
  email,
  section,
}: {
  email?: string;
  section: AdminSection;
}) {
  return (
    <aside className="admin-sidebar">
      <Link className="admin-brand" to="/admin" aria-label="STYK, админ-панель">
        <BrandLogo provider="styk" size="lg" decorative />
        <span>
          <strong>STYK</strong>
          <small>Админ-панель</small>
        </span>
      </Link>
      <nav className="admin-nav" aria-label="Админ-панель">
        {adminNavigation.map((item) =>
          item.section ? (
            <Link
              key={item.label}
              className="admin-nav-item"
              to={
                item.section === "overview"
                  ? "/admin"
                  : `/admin?section=${item.section}`
              }
              aria-current={section === item.section ? "page" : undefined}
            >
              <Icon name={item.icon} /> {item.label}
            </Link>
          ) : (
            <span
              key={item.label}
              className="admin-nav-item admin-nav-unavailable"
              aria-disabled="true"
              title="Раздел пока недоступен"
            >
              <Icon name={item.icon} /> {item.label}
            </span>
          ),
        )}
      </nav>
      <Link className="admin-nav-return" to="/">
        <Icon name="chevron-left" size="button" /> Рабочее пространство
      </Link>
      {email && (
        <div className="admin-profile">
          <span className="admin-avatar" aria-hidden="true">
            {email[0]?.toUpperCase()}
          </span>
          <div>
            <strong>Администратор</strong>
            <small>{email}</small>
          </div>
        </div>
      )}
    </aside>
  );
}

export function PlatformAdmin({
  sessionKey,
  email,
  accountID,
}: {
  sessionKey: string;
  email?: string;
  accountID?: string;
}) {
  const [params] = useSearchParams();
  const requested = params.get("section");
  const section =
    requested === "users" ||
    requested === "integrations" ||
    requested === "plans"
      ? requested
      : "overview";
  const [days, setDays] = useState("30");
  const overview = useQuery({
    queryKey: ["platform-admin", sessionKey, "overview", days],
    queryFn: ({ signal }) =>
      api<Overview>(`/api/admin/overview?days=${days}`, signal),
    retry: false,
  });
  const blocked =
    overview.error instanceof ApiError &&
    [401, 403].includes(overview.error.status);
  const data = overview.data;
  return (
    <div className="platform-admin">
      <AdminSidebar
        email={data && !blocked ? email : undefined}
        section={section}
      />
      <main className="admin-main">
        <header className="admin-heading">
          <div>
            <h1>{sections[section].title}</h1>
            <p>{sections[section].description}</p>
          </div>
          {(section === "overview" || section === "integrations") && (
            <AdminSelect
              icon="calendar"
              aria-label="Период импорта"
              value={days}
              onChange={(event) => setDays(event.target.value)}
            >
              <option value="7">Последние 7 дней</option>
              <option value="30">Последние 30 дней</option>
              <option value="90">Последние 90 дней</option>
            </AdminSelect>
          )}
        </header>
        {overview.isPending && (
          <p className="admin-card admin-state" role="status">
            Загружаем обзор…
          </p>
        )}
        {overview.isError && (
          <p className="admin-card admin-state" role="alert">
            {blocked
              ? "Доступ к админ-панели закрыт."
              : "Не удалось загрузить обзор."}
            {!blocked && (
              <button
                className="admin-button"
                onClick={() => void overview.refetch()}
              >
                Повторить
              </button>
            )}
          </p>
        )}
        {data && !blocked && section === "plans" && (
          <AdminPlans sessionKey={sessionKey} />
        )}
        {data && !blocked && section === "users" && (
          <CompanyTable sessionKey={sessionKey} accountID={accountID} />
        )}
        {data && !blocked && section === "integrations" && (
          <div className="admin-grid admin-integrations-grid">
            <ProviderBreakdown overview={data} />
            <ImportIssues issues={data.issues} />
          </div>
        )}
        {data && !blocked && section === "overview" && (
          <>
            <section
              className="admin-card admin-metrics"
              aria-label="Показатели платформы"
            >
              {metrics.map(({ key, title, icon }) => (
                <div className="admin-metric" key={key}>
                  <span
                    className={
                      key === "errors"
                        ? "admin-metric-icon admin-metric-error"
                        : "admin-metric-icon"
                    }
                  >
                    <Icon name={icon} size="metric" />
                  </span>
                  <div>
                    <p>{title}</p>
                    <strong>{number.format(data[key])}</strong>
                    <small>
                      {key === "companies" || key === "connections"
                        ? "Сейчас"
                        : "За выбранный период"}
                    </small>
                  </div>
                </div>
              ))}
            </section>
            <div className="admin-grid">
              <CompanyTable sessionKey={sessionKey} accountID={accountID} />
              <ProviderBreakdown overview={data} />
              <ImportIssues issues={data.issues} />
            </div>
          </>
        )}
      </main>
    </div>
  );
}

function ImportIssues({ issues }: { issues: Overview["issues"] }) {
  return (
    <section className="admin-card admin-attention">
      <h2>
        Требуют внимания
        {issues.length > 0 && (
          <span className="admin-badge admin-badge-alert">
            {number.format(issues.length)}
          </span>
        )}
      </h2>
      {!issues.length ? (
        <p className="admin-state">
          За выбранный период нет подключений с последним неудачным импортом.
        </p>
      ) : (
        <div className="admin-issues">
          {issues.map((issue) => (
            <article key={issue.id}>
              <span className="admin-issue-icon">
                <Icon name="warning" size="button" />
              </span>
              <div>
                <strong>{issue.name}</strong>
                <p>
                  {providerName(issue.provider)} ·{" "}
                  {reasons[issue.reason] ?? "Ошибка импорта"}
                </p>
                <small>{dateTime.format(new Date(issue.finished_at))}</small>
              </div>
            </article>
          ))}
        </div>
      )}
    </section>
  );
}

function ProviderBreakdown({ overview }: { overview: Overview }) {
  let offset = 0;
  const rows = providerEntries(overview.providers).map(([id, count]) => {
    const color = providerColors[id] ?? "var(--styk-primary)";
    const share = (count / overview.connections) * 100;
    const slice = `${color} ${offset}% ${offset + share}%`;
    offset += share;
    return { id, count, share, color, slice };
  });
  return (
    <section className="admin-card admin-providers">
      <h2>Интеграции по платформам</h2>
      {!rows.length ? (
        <p className="admin-state">Пока нет подключений.</p>
      ) : (
        <div className="admin-provider-content">
          <div
            className="admin-donut"
            aria-hidden="true"
            style={{
              background: `conic-gradient(${rows.map(({ slice }) => slice).join(",")})`,
            }}
          >
            <span>
              <strong>{number.format(overview.connections)}</strong>
              <small>подключений</small>
            </span>
          </div>
          <ul>
            {rows.map(({ id, count, share, color }) => (
              <li key={id}>
                <span>
                  <i style={{ background: color }} />
                  {providerName(id)}
                </span>
                <span>
                  <b>{number.format(count)}</b> (
                  {number.format(Math.round(share))}%)
                </span>
              </li>
            ))}
          </ul>
        </div>
      )}
    </section>
  );
}

function CompanyTable({
  sessionKey,
  accountID,
}: {
  sessionKey: string;
  accountID?: string;
}) {
  const [q, setQuery] = useState("");
  const [status, setStatus] = useState("");
  const [page, setPage] = useState(1);
  const [limit, setLimit] = useState(20);
  const support = useMutation({
    mutationFn: (tenantID: string) =>
      post<void>("/api/admin/support", { tenant_id: tenantID }),
    onSuccess: () => window.location.assign("/"),
  });
  const query = useQuery({
    queryKey: ["platform-admin", sessionKey, "users", q, status, page, limit],
    queryFn: ({ signal }) =>
      api<Users>(
        `/api/admin/users?${new URLSearchParams({ q, status, page: String(page), limit: String(limit) })}`,
        signal,
      ),
    retry: false,
  });
  const data = query.data;
  return (
    <section className="admin-card admin-companies">
      <h2>
        Компании{" "}
        {data && (
          <span className="admin-badge">{number.format(data.total)}</span>
        )}
      </h2>
      <div className="admin-filters">
        <label className="admin-search">
          <Icon name="search" size="button" className="admin-control-leading" />
          <input
            className="admin-control"
            type="search"
            aria-label="Поиск компании"
            placeholder="Поиск по названию, email или ID…"
            value={q}
            maxLength={128}
            onChange={(event) => {
              setQuery(event.target.value);
              setPage(1);
            }}
          />
        </label>
        <AdminSelect
          aria-label="Статус аккаунта"
          value={status}
          onChange={(event) => {
            setStatus(event.target.value);
            setPage(1);
          }}
        >
          <option value="">Все статусы</option>
          {Object.entries(statuses).map(([id, label]) => (
            <option key={id} value={id}>
              {label}
            </option>
          ))}
        </AdminSelect>
      </div>
      {query.isPending && (
        <p className="admin-state" role="status">
          Загружаем компании…
        </p>
      )}
      {support.isError && (
        <p className="admin-state" role="alert">
          Не удалось открыть кабинет для поддержки. Проверьте, что аккаунт
          активен.
        </p>
      )}
      {query.isError && (
        <p className="admin-state" role="alert">
          Не удалось загрузить компании.
          <button className="admin-button" onClick={() => void query.refetch()}>
            Повторить
          </button>
        </p>
      )}
      {data && (
        <>
          {data.users.length ? (
            <CompanyRows
              users={data.users}
              accountID={accountID}
              supportPending={support.isPending}
              onSupport={(tenantID) => support.mutate(tenantID)}
            />
          ) : (
            <p className="admin-state">Компании не найдены.</p>
          )}
          <CompanyPager
            data={data}
            page={page}
            limit={limit}
            onPageChange={setPage}
            onLimitChange={(size) => {
              setLimit(size);
              setPage(1);
            }}
          />
        </>
      )}
    </section>
  );
}

function CompanyRows({
  users,
  accountID,
  supportPending,
  onSupport,
}: {
  users: Users["users"];
  accountID?: string;
  supportPending: boolean;
  onSupport: (tenantID: string) => void;
}) {
  return (
    <div className="admin-table-scroll">
      <table aria-label="Компании STYK">
        <thead>
          <tr>
            <th scope="col">Компания / владелец</th>
            <th scope="col" className="admin-connection-total">
              Подключения
            </th>
            <th scope="col">Интеграции</th>
            <th scope="col">Статус</th>
            <th scope="col" className="admin-support-column">
              Вход
            </th>
          </tr>
        </thead>
        <tbody>
          {users.map((company) => (
            <tr key={company.id}>
              <td>
                <strong>{company.name}</strong>
                <small>{company.email}</small>
              </td>
              <td className="admin-connection-total">
                {number.format(company.connections)}
              </td>
              <td>
                <div className="admin-provider-counts">
                  {providerEntries(company.providers).map(([id, count]) => (
                    <span key={id} title={providerName(id)}>
                      {id === "moysklad" || id === "ozon" ? (
                        <BrandLogo provider={id} size="xs" />
                      ) : (
                        providerName(id)
                      )}
                      {number.format(count)}
                    </span>
                  ))}
                  {!company.connections && (
                    <span aria-label="Нет интеграций">—</span>
                  )}
                </div>
                {company.connections > 0 && (
                  <small className="admin-compact-connection-total">
                    Всего: {number.format(company.connections)}
                  </small>
                )}
              </td>
              <td>
                <span className={`admin-status admin-status-${company.status}`}>
                  {statuses[company.status]}
                </span>
              </td>
              <td className="admin-support-column">
                {company.id !== accountID && company.status === "active" && (
                  <button
                    type="button"
                    className="admin-button admin-support-open"
                    aria-label={`Войти для поддержки: ${company.email}`}
                    disabled={supportPending}
                    onClick={() => onSupport(company.id)}
                  >
                    <Icon name="arrow" size="button" />
                    <span>Войти</span>
                  </button>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function CompanyPager({
  data,
  page,
  limit,
  onPageChange,
  onLimitChange,
}: {
  data: { total: number; users: readonly unknown[] };
  page: number;
  limit: number;
  onPageChange: (page: number) => void;
  onLimitChange: (size: number) => void;
}) {
  const first = (page - 1) * limit + 1;
  const last = first + data.users.length - 1;
  const range =
    first === last
      ? number.format(first)
      : `${number.format(first)}–${number.format(last)}`;
  const pageCount = Math.max(1, Math.ceil(data.total / limit));
  const start = Math.max(1, Math.min(page - 1, pageCount - 2));
  const middle = Array.from(
    { length: Math.min(3, pageCount) },
    (_, index) => start + index,
  );
  const pages = [...new Set([1, ...middle, pageCount])];
  return (
    <div className="admin-pager">
      <span className="admin-page-range">
        {data.users.length
          ? `Показано ${range} из ${number.format(data.total)}`
          : `Всего: ${number.format(data.total)}`}
      </span>
      <nav className="admin-page-buttons" aria-label="Страницы компаний">
        <button
          className="admin-button"
          aria-label="Предыдущая страница"
          disabled={page === 1}
          onClick={() => onPageChange(page - 1)}
        >
          <Icon name="chevron-left" size="button" />
        </button>
        {pages.map((value, index) => (
          <Fragment key={value}>
            {index > 0 && value - pages[index - 1] > 1 && (
              <span className="admin-page-gap" aria-hidden="true">
                …
              </span>
            )}
            <button
              className="admin-button"
              aria-current={page === value ? "page" : undefined}
              aria-label={`Страница ${value}`}
              onClick={() => onPageChange(value)}
            >
              {value}
            </button>
          </Fragment>
        ))}
        <button
          className="admin-button"
          aria-label="Следующая страница"
          disabled={page >= pageCount}
          onClick={() => onPageChange(page + 1)}
        >
          <Icon name="chevron-right" size="button" />
        </button>
      </nav>
      <label className="admin-page-size">
        На странице
        <AdminSelect
          value={limit}
          onChange={(event) => onLimitChange(Number(event.target.value))}
        >
          {[10, 20, 50].map((size) => (
            <option key={size} value={size}>
              {size}
            </option>
          ))}
        </AdminSelect>
      </label>
    </div>
  );
}

type PlanCompany = {
  id: string;
  name: string;
  email: string;
  code: string;
  revision: number;
  providers: Counts;
  used: number;
  exceeded_plans: string[];
};

type PlanCompanies = {
  total: number;
  companies: PlanCompany[];
  plans: MarketplacePlanLimits[];
};

function AdminPlans({ sessionKey }: { sessionKey: string }) {
  const client = useQueryClient();
  const [search, setSearch] = useState("");
  const [page, setPage] = useState(1);
  const [limit, setLimit] = useState(20);
  const queryKey = ["platform-admin", sessionKey, "plans"];
  const query = useQuery({
    queryKey: [...queryKey, search, page, limit],
    queryFn: ({ signal }) =>
      api<PlanCompanies>(
        "/api/admin/plans?" +
          new URLSearchParams({
            q: search,
            page: String(page),
            limit: String(limit),
          }),
        signal,
      ),
    retry: false,
  });
  const save = useMutation({
    mutationFn: ({ company, code }: { company: PlanCompany; code: string }) =>
      post<MarketplacePlan>(
        "/api/admin/plans/" + encodeURIComponent(company.id),
        {
          code,
          expected_revision: company.revision,
        },
      ),
    onSettled: async () => {
      await Promise.all([
        client.invalidateQueries({ queryKey }),
        client.invalidateQueries({ queryKey: [marketplacePlanKey] }),
      ]);
    },
  });
  const data = query.data;
  return (
    <section
      className="admin-card admin-companies"
      aria-label="Тарифы компаний"
    >
      <h2>Тарифы компаний</h2>
      <div className="admin-filters">
        <label className="admin-search">
          <Icon name="search" size="button" className="admin-control-leading" />
          <input
            className="admin-control"
            type="search"
            aria-label="Поиск компании"
            placeholder="Поиск по названию, email или ID…"
            maxLength={128}
            value={search}
            onChange={(event) => {
              setSearch(event.target.value);
              setPage(1);
            }}
          />
        </label>
      </div>
      {query.isPending && (
        <p className="admin-state" role="status">
          Загружаем тарифы…
        </p>
      )}
      {query.isError && (
        <p className="admin-state" role="alert">
          Не удалось загрузить тарифы.{" "}
          <button
            type="button"
            className="admin-button"
            onClick={() => void query.refetch()}
          >
            Повторить
          </button>
        </p>
      )}
      {save.isError && (
        <p className="admin-state" role="alert">
          {save.error instanceof ApiError &&
          save.error.code === "PLAN_REVISION_CONFLICT"
            ? "Тариф уже изменился. Проверьте актуальное назначение и выберите тариф заново."
            : "Не удалось подтвердить сохранение. Проверьте текущий тариф перед повторной попыткой."}
        </p>
      )}
      {save.isSuccess && (
        <p className="admin-state" role="status">
          Тариф сохранён.
        </p>
      )}
      {data &&
        (data.companies.length > 0 ? (
          <div className="admin-table-wrap">
            <table className="admin-table">
              <thead>
                <tr>
                  <th scope="col">Компания</th>
                  <th scope="col">Занято мест</th>
                  <th scope="col">Тариф и доступ</th>
                </tr>
              </thead>
              <tbody>
                {data.companies.map((company) => (
                  <PlanCompanyRow
                    key={company.id + ":" + company.revision}
                    company={company}
                    plans={data.plans}
                    pending={
                      save.isPending || query.isFetching || query.isError
                    }
                    onSave={(code) => save.mutate({ company, code })}
                  />
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <p className="admin-state">Компании не найдены.</p>
        ))}
      {data && (
        <CompanyPager
          data={{ total: data.total, users: data.companies }}
          page={page}
          limit={limit}
          onPageChange={setPage}
          onLimitChange={(next) => {
            setPage(1);
            setLimit(next);
          }}
        />
      )}
    </section>
  );
}

function PlanCompanyRow({
  company,
  plans,
  pending,
  onSave,
}: {
  company: PlanCompany;
  plans: MarketplacePlanLimits[];
  pending: boolean;
  onSave: (code: string) => void;
}) {
  const [selected, setSelected] = useState(company.code);
  const current = plans.find((plan) => plan.code === company.code);
  const currentExceeded = company.exceeded_plans.includes(company.code);
  const willExceed =
    selected !== company.code && company.exceeded_plans.includes(selected);
  return (
    <tr>
      <th scope="row">
        <strong>{company.name}</strong>
        <small>{company.email}</small>
      </th>
      <td>
        <strong>
          {company.used} из {current?.total_limit}
        </strong>
        {Object.entries(company.providers).map(([provider, count]) => (
          <small key={provider}>
            {providerNames[provider] ?? provider} · {count}
          </small>
        ))}
        {currentExceeded && (
          <small className="admin-plan-warning">Лимит превышен</small>
        )}
      </td>
      <td>
        <div className="admin-plan-editor">
          <AdminSelect
            aria-label={"Тариф компании " + company.name}
            value={selected}
            disabled={pending}
            onChange={(event) => setSelected(event.target.value)}
          >
            {plans.map((plan) => (
              <option key={plan.code} value={plan.code}>
                {marketplacePlanName(plan.code)} · {plan.total_limit}
                {plan.provider_limit < plan.total_limit
                  ? " (по одному на площадку)"
                  : ""}
              </option>
            ))}
          </AdminSelect>
          <button
            className="admin-button"
            type="button"
            disabled={pending || selected === company.code}
            onClick={() => onSave(selected)}
            aria-label={"Сохранить тариф компании " + company.name}
          >
            Сохранить
          </button>
        </div>
        {willExceed && (
          <small className="admin-plan-warning" role="status">
            Подключения сохранятся. Новые будут недоступны до устранения
            превышения.
          </small>
        )}
      </td>
    </tr>
  );
}
