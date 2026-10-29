import { Integrations } from "./Integrations";
import {
  CredentialNotice,
  useCredentialSubmission,
} from "./CredentialSubmission";
import { StockSyncNotice, useStockSyncSubmission } from "./StockSyncControls";
import {
  ImportSubmissionNotice,
  useImportSubmission,
} from "./ImportSubmission";
import { ProductMappingPage } from "./ProductMappingPage";
import { WarehouseReview } from "./WarehouseReview";
import {
  ConfirmationNotice,
  useConfirmation,
  type Confirmation,
} from "./Confirmation";
import { useEffect, useState, type FormEvent } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  NavLink,
  Route,
  Routes,
  useNavigate,
  useLocation,
} from "react-router-dom";
import {
  api,
  params,
  type Account,
  type Batch,
  type Page,
  type Report,
  type Stock,
  type Warehouse,
} from "./api";

type Scope = { tenant: string; moy: string; ozon: string };
const stateLabels: Record<string, string> = {
  PASS: "В порядке",
  WARN: "Нужно проверить",
  FAIL: "Ошибка",
  NOT_CHECKED: "Не проверено",
};
function State({ status }: { status: string }) {
  return (
    <span className={"read-state " + status.toLowerCase()}>
      {stateLabels[status] || "Требуется проверка"}
    </span>
  );
}
function Notice({ children }: { children: React.ReactNode }) {
  return <div className="notice">{children}</div>;
}
function Loading() {
  return (
    <div className="notice" role="status">
      Загружаем данные…
    </div>
  );
}
function Failure({ error }: { error: unknown }) {
  return (
    <div className="error" role="alert">
      {error instanceof Error ? error.message : "Не удалось загрузить данные."}
    </div>
  );
}
function scopeParams(s: Scope) {
  return params({
    tenant: s.tenant,
    moysklad_account: s.moy,
    ozon_account: s.ozon,
  });
}

function Login({ onDone }: { onDone: () => void }) {
  const [secret, setSecret] = useState(""),
    [error, setError] = useState(""),
    [busy, setBusy] = useState(false);
  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      const r = await fetch("/api/session", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ secret }),
      });
      setSecret("");
      if (!r.ok)
        throw new Error(
          r.status === 401
            ? "Ключ входа не принят."
            : "Не удалось войти. Проверьте доступность STYK.",
        );
      onDone();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Не удалось войти.");
    } finally {
      setBusy(false);
    }
  }
  return (
    <main className="login">
      <div className="login-mark">
        S<span>TYK</span>
      </div>
      <div className="login-panel">
        <p className="eyebrow">Рабочее пространство</p>
        <h1>Войти в STYK</h1>
        <p>
          Подключайте сервисы, загружайте данные и проверяйте соответствия
          товаров.
        </p>
        <form onSubmit={submit}>
          <label htmlFor="secret">Ключ входа</label>
          <input
            id="secret"
            type="password"
            autoComplete="off"
            value={secret}
            onChange={(e) => setSecret(e.target.value)}
            required
          />
          <button disabled={busy}>{busy ? "Входим…" : "Войти"}</button>
          {error && (
            <div className="error" role="alert">
              {error}
            </div>
          )}
        </form>
      </div>
      <p className="login-footer">STYK · Рабочее пространство</p>
    </main>
  );
}
function SourceCard({
  name,
  batch,
  status,
}: {
  name: string;
  batch?: Batch;
  status: string;
}) {
  const future =
    !!batch?.present &&
    (batch.age?.startsWith("-") ||
      (!!batch.observed_at && Date.parse(batch.observed_at) > Date.now()));
  const stale =
    !!batch?.present &&
    !!batch.observed_at &&
    Date.now() - Date.parse(batch.observed_at) > 3600000;
  return (
    <article className="read-source-card">
      <header>
        <h2>{name}</h2>
        <State status={status} />
      </header>
      <p>
        {batch?.present
          ? batch.completed
            ? "Данные загружены"
            : "Загрузка не завершена"
          : "Данных пока нет"}
      </p>
      {batch?.present && batch.observed_at && (
        <time dateTime={batch.observed_at}>
          Загрузка: {new Date(batch.observed_at).toLocaleString("ru-RU")}
        </time>
      )}
      {future && (
        <p className="read-warning">
          Время загрузки указано в будущем. Давность данных неизвестна.
        </p>
      )}
      {stale && <p className="read-warning">Данные могут быть устаревшими.</p>}
      <footer>
        {batch?.counts?.products !== undefined && (
          <span>Товаров: {batch.counts.products}</span>
        )}
        {batch?.counts?.variants !== undefined && (
          <span>Вариантов: {batch.counts.variants}</span>
        )}
        {batch?.counts?.warehouses !== undefined && (
          <span>Складов: {batch.counts.warehouses}</span>
        )}
      </footer>
      <details className="read-details">
        <summary>Подробнее о загрузке</summary>
        <p>Проверка доступа выполняется отдельно на странице интеграций.</p>
        {batch?.counts?.stock_rows !== undefined && (
          <p>
            Записей об остатках: {batch.counts.stock_rows}. Отсутствие записи не
            означает нулевой остаток.
          </p>
        )}
      </details>
    </article>
  );
}
export function Overview({
  report,
  loading,
  error,
}: {
  report?: Report;
  loading: boolean;
  error: unknown;
}) {
  if (loading) return <Loading />;
  if (error) return <Failure error={error} />;
  if (!report)
    return (
      <Notice>
        Выберите оба зарегистрированных аккаунта, чтобы увидеть загруженные
        данные.
      </Notice>
    );
  return (
    <div className="read-page">
      <header className="read-heading">
        <h1>Обзор</h1>
        <p>Последние загруженные данные выбранных аккаунтов.</p>
      </header>
      <div className="read-source-grid">
        <SourceCard
          name="МойСклад"
          batch={report.batches.moysklad}
          status={
            report.checks.find((c) => c.code === "MOYSKLAD_IMPORT")?.status ??
            "NOT_CHECKED"
          }
        />
        <SourceCard
          name="Ozon"
          batch={report.batches.ozon}
          status={
            report.checks.find((c) => c.code === "OZON_IMPORT")?.status ??
            "NOT_CHECKED"
          }
        />
      </div>
      <section className="read-next">
        <h2>Следующий шаг</h2>
        <p>
          Проверьте соответствия товаров и складов. Загрузка данных не меняет
          остатки в сервисах.
        </p>
        <nav>
          <NavLink to="/integrations">Настроить интеграции</NavLink>
          <NavLink to="/products">Сопоставить товары</NavLink>
          <NavLink to="/warehouses">Сопоставить склады</NavLink>
          <NavLink to="/diagnostics">Диагностика</NavLink>
        </nav>
      </section>
    </div>
  );
}
export function Products({
  scope,
  confirmation,
}: {
  scope: Scope;
  confirmation?: Confirmation;
}) {
  return <ProductMappingPage scope={scope} confirmation={confirmation} />;
}

export function StockRows({
  scope,
  source,
  account,
  warehouse,
}: {
  scope: Scope;
  source: string;
  account: string;
  warehouse: string;
}) {
  const [offset, setOffset] = useState(0);
  const q = useQuery({
    queryKey: ["stock", scope.tenant, source, account, warehouse, offset],
    queryFn: ({ signal }) =>
      api<Page<Stock>>(
        "/api/stock?" +
          params({ tenant: scope.tenant, source, account, warehouse, offset }),
        signal,
      ),
  });
  if (q.isLoading) return <Loading />;
  if (q.error) return <Failure error={q.error} />;
  if (!q.data?.items.length)
    return <Notice>Данных об остатках нет. Количество неизвестно.</Notice>;
  const value = (x: string | null) => (x === null ? "Неизвестно" : x);
  return (
    <>
      <div className="stock-list">
        {q.data.items.map((s) => (
          <div className="stock-row" key={s.kind + s.item_id}>
            <strong>
              {s.kind === "variant" ? "Вариант" : "Товар"} · {s.item_id}
            </strong>
            <small>
              {source === "moysklad"
                ? `На складе ${value(s.stock)} · В резерве ${value(s.reserve)} · Ожидается ${value(s.incoming)} · Доступно ${value(s.quantity)}`
                : `На складе ${value(s.present)} · В резерве ${value(s.reserved)} · Свободно ${value(s.free_stock)}`}
            </small>
            <small>
              {Date.now() - Date.parse(s.observed_at) > 3600000
                ? "Данные могут быть устаревшими"
                : "Получено"}{" "}
              · {new Date(s.observed_at).toLocaleString("ru-RU")}
            </small>
          </div>
        ))}
      </div>
      <div className="pagination">
        <button
          className="secondary"
          disabled={offset === 0}
          onClick={() => setOffset(Math.max(0, offset - 50))}
        >
          Назад
        </button>
        <span>
          Строки {offset + 1}–{offset + q.data.items.length}
        </span>
        <button
          className="secondary"
          disabled={!q.data.has_next}
          onClick={() => setOffset(offset + 50)}
        >
          Далее
        </button>
      </div>
    </>
  );
}
function WarehouseList({
  scope,
  source,
  account,
  other,
  selectWarehouse,
  chosen,
}: {
  scope: Scope;
  source: string;
  account: string;
  other: string;
  selectWarehouse: (id: string) => void;
  chosen?: string;
}) {
  const [offset, setOffset] = useState(0),
    [selected, setSelected] = useState("");
  const q = useQuery({
    queryKey: ["warehouses", scope.tenant, source, account, other, offset],
    queryFn: ({ signal }) =>
      api<Page<Warehouse>>(
        "/api/warehouses?" +
          params({
            tenant: scope.tenant,
            source,
            account,
            other_account: other,
            offset,
          }),
        signal,
      ),
    enabled: !!scope.tenant && !!account,
  });
  return (
    <section className="warehouse-panel read-warehouse-list">
      <h2>{source === "moysklad" ? "МойСклад" : "Ozon"}</h2>
      {!account ? (
        <Notice>Аккаунт не выбран.</Notice>
      ) : q.isLoading ? (
        <Loading />
      ) : q.error ? (
        <Failure error={q.error} />
      ) : !q.data?.items.length ? (
        <Notice>Склады ещё не загружены.</Notice>
      ) : (
        <>
          {q.data.items.map((w) => (
            <div className="warehouse-row" key={w.id}>
              <div>
                <strong>{w.name || "Склад без названия"}</strong>
                <details className="read-details">
                  <summary>Данные склада</summary>
                  <p>
                    ID: <code>{w.id}</code>
                  </p>
                  <p>Записей об остатках: {w.stock_rows}</p>
                </details>
                <button
                  className="link-button"
                  onClick={() => setSelected(selected === w.id ? "" : w.id)}
                >
                  {selected === w.id ? "Скрыть остатки" : "Посмотреть остатки"}
                </button>
                <button
                  className="link-button"
                  aria-pressed={chosen === w.id}
                  onClick={() => selectWarehouse(w.id)}
                >
                  {chosen === w.id
                    ? "Выбран для сопоставления"
                    : "Выбрать для сопоставления"}
                </button>
                {selected === w.id && (
                  <StockRows
                    key={w.id}
                    scope={scope}
                    source={source}
                    account={account}
                    warehouse={w.id}
                  />
                )}
              </div>
              <div className="warehouse-meta">
                <span>
                  {source === "ozon"
                    ? w.is_rfbs === null
                      ? "Тип неизвестен"
                      : w.is_rfbs
                        ? "rFBS"
                        : "FBS"
                    : "Склад МойСклад"}
                </span>
                <small>
                  {w.mapped === null
                    ? "Соответствие не проверено"
                    : w.mapped
                      ? "Соответствие подтверждено"
                      : "Соответствие не задано"}
                </small>
                <small>
                  {w.stock_rows === 0
                    ? "Остатки неизвестны"
                    : "Данные об остатках получены"}
                </small>
              </div>
            </div>
          ))}
          <div className="pagination">
            <button
              className="secondary"
              disabled={offset === 0}
              onClick={() => {
                setOffset(Math.max(0, offset - 50));
                setSelected("");
              }}
            >
              Назад
            </button>
            <button
              className="secondary"
              disabled={!q.data.has_next}
              onClick={() => {
                setOffset(offset + 50);
                setSelected("");
              }}
            >
              Далее
            </button>
          </div>
        </>
      )}
    </section>
  );
}
export function Warehouses({
  scope,
  confirmation,
}: {
  scope: Scope;
  confirmation?: Confirmation;
}) {
  const [moyWarehouse, setMoyWarehouse] = useState("");
  const [ozonWarehouse, setOzonWarehouse] = useState("");
  return (
    <div className="read-page warehouse-mapping-page">
      <div className="read-heading">
        <div>
          <h1>Сопоставление складов</h1>
          <p className="subtitle">
            Выберите склад МойСклад и соответствующий ему склад Ozon.
          </p>
        </div>
      </div>
      <Notice>
        Отсутствие данных об остатках не означает нулевой остаток. Совпадение
        названий складов само по себе не подтверждает соответствие.
      </Notice>
      <div className="warehouse-grid">
        <WarehouseList
          scope={scope}
          source="moysklad"
          account={scope.moy}
          other={scope.ozon}
          selectWarehouse={setMoyWarehouse}
          chosen={moyWarehouse}
        />
        <WarehouseList
          scope={scope}
          source="ozon"
          account={scope.ozon}
          other={scope.moy}
          selectWarehouse={setOzonWarehouse}
          chosen={ozonWarehouse}
        />
      </div>
      {moyWarehouse && ozonWarehouse && (
        <WarehouseReview
          key={`${moyWarehouse}:${ozonWarehouse}`}
          scope={scope}
          moy={moyWarehouse}
          ozon={ozonWarehouse}
          confirmation={confirmation}
          close={() => {
            setMoyWarehouse("");
            setOzonWarehouse("");
          }}
        />
      )}
    </div>
  );
}
const checkTitles: Record<string, string> = {
  ACCOUNT_REGISTRATION: "Аккаунты сервисов",
  MOYSKLAD_REGISTRATION: "Аккаунт МойСклад",
  OZON_REGISTRATION: "Аккаунт Ozon",
  MOYSKLAD_CONNECTION: "Доступ к МойСклад",
  OZON_CONNECTION: "Доступ к Ozon",
  MOYSKLAD_IMPORT: "Загрузка МойСклад",
  OZON_IMPORT: "Загрузка Ozon",
  POSTING_IMPORT: "Отправления Ozon",
  CUSTOMER_ORDER_IMPORT: "Заказы МойСклад",
  MOYSKLAD_STOCK_COVERAGE: "Остатки МойСклад",
  OZON_STOCK_COVERAGE: "Остатки Ozon",
  PRODUCT_MAPPINGS: "Соответствия товаров",
  WAREHOUSE_MAPPINGS: "Соответствия складов",
  ORDER_CORRELATIONS: "Связи заказов",
  MATCHING_SUGGESTIONS: "Предложения соответствий",
  CALCULATION_SCOPE: "Проверка расчёта остатков",
  EXISTING_EXPOSURE: "Размещённые товары на маркетплейсе",
  COVERAGE: "Полнота данных",
  SOURCE_ORDERING: "Порядок изменений",
  SOURCE_PERMISSIONS: "Разрешения аккаунтов",
  RESERVATION_ATTRIBUTION: "Учёт резервов",
};
function checkSummary(code: string, status: string) {
  if (status === "PASS") return "Проверка пройдена для выбранных аккаунтов.";
  if (code.endsWith("_CONNECTION"))
    return "Проверить доступ можно на странице интеграций.";
  if (code === "PRODUCT_MAPPINGS")
    return "Проверьте и подтвердите соответствия товаров.";
  if (code === "WAREHOUSE_MAPPINGS")
    return "Проверьте и подтвердите соответствия складов.";
  if (code.endsWith("_IMPORT"))
    return status === "FAIL"
      ? "Данные загрузки неполные. Проверьте историю обновлений."
      : "Успешная загрузка отсутствует или данные устарели.";
  if (code.endsWith("_REGISTRATION") || code === "ACCOUNT_REGISTRATION")
    return "Оба аккаунта должны быть добавлены в выбранное рабочее пространство.";
  if (code.includes("STOCK_COVERAGE") || code === "COVERAGE")
    return "Полученные записи не подтверждают полноту остатков. Отсутствующие значения неизвестны.";
  if (code === "EXISTING_EXPOSURE")
    return "Товары могут оставаться доступными на маркетплейсе. Диагностика не изменяет их остатки.";
  if (code === "RESERVATION_ATTRIBUTION")
    return "Связь заказов с резервами и исходными остатками не подтверждена.";
  if (code === "SOURCE_ORDERING")
    return "Порядок изменений в источниках не подтверждён.";
  if (code === "SOURCE_PERMISSIONS")
    return "Полнота разрешений и видимость всех данных не проверены.";
  if (code === "CALCULATION_SCOPE")
    return status === "NOT_CHECKED"
      ? "Расчёт для подтверждённой пары товара и склада не выполнялся."
      : "Проверена только одна пара товара и склада. Другие пары не оценивались.";
  if (code === "ORDER_CORRELATIONS")
    return "Предполагаемая связь заказов требует отдельного подтверждения.";
  if (code === "MATCHING_SUGGESTIONS")
    return "Предложения помогают выбрать товар и требуют вашего подтверждения.";
  return status === "NOT_CHECKED"
    ? "Эта проверка пока не выполнялась."
    : "Проверьте подробности перед следующим действием.";
}
export function Diagnostics({
  report,
  loading,
  error,
}: {
  report?: Report;
  loading: boolean;
  error: unknown;
}) {
  const [copyStatus, setCopyStatus] = useState("");
  if (loading) return <Loading />;
  if (error) return <Failure error={error} />;
  if (!report)
    return (
      <Notice>
        Выберите оба зарегистрированных аккаунта, чтобы открыть диагностику.
      </Notice>
    );
  const decisionLabels: Record<string, string> = {
    NOT_CHECKED: "Проверка не выполнялась",
    UNKNOWN: "Недостаточно данных",
    BLOCKED: "Есть ограничения",
    READY_CONDITIONAL: "Требуется дополнительная проверка",
  };
  return (
    <div className="read-page diagnostics-page">
      <header className="read-heading">
        <h1>Диагностика</h1>
        <p>
          Состояние данных выбранных аккаунтов. Ключи доступа в этот отчёт не
          входят.
        </p>
      </header>
      <section className="read-next">
        <h2>
          {decisionLabels[report.decision_status] ||
            "Проверьте состояние данных"}
        </h2>
        <p>Диагностика не запускает синхронизацию и не изменяет остатки.</p>
        <button
          className="primary-button"
          onClick={async () => {
            try {
              await navigator.clipboard.writeText(
                JSON.stringify(report, null, 2),
              );
              setCopyStatus("Отчёт скопирован");
            } catch {
              setCopyStatus(
                "Не удалось скопировать отчёт. Откройте технические детали и скопируйте нужные данные вручную.",
              );
            }
          }}
        >
          Скопировать для поддержки
        </button>
        {copyStatus && <p role="status">{copyStatus}</p>}
      </section>
      <section className="read-checks" aria-label="Проверки данных">
        {report.checks.map((check) => (
          <article className="read-check" key={check.code}>
            <div>
              <h2>{checkTitles[check.code] || "Дополнительная проверка"}</h2>
              <p>{checkSummary(check.code, check.status)}</p>
              <details className="read-details">
                <summary>Технические детали</summary>
                <p>
                  <code>{check.code}</code>
                </p>
                <p>{check.detail}</p>
              </details>
            </div>
            <State status={check.status} />
          </article>
        ))}
      </section>
      <details className="read-details read-report-details">
        <summary>Технические данные отчёта</summary>
        <p>
          Результат расчёта: <code>{report.decision_status}</code>
        </p>
        {report.decision_reasons.map((reason) => (
          <p key={reason}>
            <code>{reason}</code>
          </p>
        ))}
        <pre>{JSON.stringify(report, null, 2)}</pre>
      </details>
    </div>
  );
}

function savedWorkspace() {
  try {
    const value = JSON.parse(sessionStorage.getItem("styk-workspace") || "{}");
    return {
      tenant: typeof value.tenant === "string" ? value.tenant : "",
      moy: typeof value.moy === "string" ? value.moy : "",
      ozon: typeof value.ozon === "string" ? value.ozon : "",
    };
  } catch {
    return { tenant: "", moy: "", ozon: "" };
  }
}

export function App() {
  const client = useQueryClient();
  const confirmation = useConfirmation();
  const importSubmission = useImportSubmission();
  const credentialSubmission = useCredentialSubmission();
  const stockSyncSubmission = useStockSyncSubmission();
  const navigate = useNavigate();
  const integrationPage = useLocation().pathname === "/integrations";
  useEffect(() => {
    if (
      !confirmation.pending &&
      !importSubmission.pending &&
      !credentialSubmission.pending &&
      !stockSyncSubmission.pending
    )
      return;
    const warn = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = "";
    };
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [
    confirmation.pending,
    importSubmission.pending,
    credentialSubmission.pending,
    stockSyncSubmission.pending,
  ]);
  const session = useQuery({
    queryKey: ["session"],
    queryFn: ({ signal }) => api<null>("/api/session", signal),
    retry: false,
  });
  const [initialWorkspace] = useState(savedWorkspace);
  const [tenant, setTenant] = useState(initialWorkspace.tenant),
    [chosen, setChosen] = useState(initialWorkspace.tenant),
    [moy, setMoy] = useState(initialWorkspace.moy),
    [ozon, setOzon] = useState(initialWorkspace.ozon);
  useEffect(() => {
    try {
      sessionStorage.setItem(
        "styk-workspace",
        JSON.stringify({ tenant: chosen, moy, ozon }),
      );
    } catch {
      // The console remains usable when browser storage is unavailable.
    }
  }, [chosen, moy, ozon]);
  const accounts = useQuery({
    queryKey: ["accounts", chosen],
    queryFn: ({ signal }) =>
      api<{ accounts: Account[]; truncated: boolean }>(
        "/api/registrations?" + params({ tenant: chosen }),
        signal,
      ),
    enabled: session.isSuccess && !!chosen,
  });
  const scope = { tenant: chosen, moy, ozon };
  useEffect(() => {
    if (!accounts.data) return;
    if (
      moy &&
      !accounts.data.accounts.some(
        (a) => a.source === "moysklad" && a.account === moy,
      )
    )
      setMoy("");
    if (
      ozon &&
      !accounts.data.accounts.some(
        (a) => a.source === "ozon" && a.account === ozon,
      )
    )
      setOzon("");
  }, [accounts.data, moy, ozon]);
  const report = useQuery({
    queryKey: ["diagnostics", scope],
    queryFn: ({ signal }) =>
      api<Report>("/api/diagnostics?" + scopeParams(scope), signal),
    enabled: session.isSuccess && !!chosen,
  });
  if (session.isLoading)
    return (
      <main className="login">
        <Loading />
      </main>
    );
  if (!session.isSuccess)
    return (
      <>
        {confirmation.sessionLost && (
          <p role="alert">
            Предыдущее подтверждение могло сохраниться. Войдите снова и
            проверьте историю соответствий исходной пары аккаунтов. В новой
            сессии эта проверка не повторяется.
          </p>
        )}
        {importSubmission.sessionLost && (
          <p role="alert">
            Предыдущий запрос на загрузку мог попасть в очередь. Войдите снова и
            проверьте историю исходного аккаунта.
          </p>
        )}
        <Login
          onDone={() => {
            void client.invalidateQueries({ queryKey: ["session"] });
          }}
        />
      </>
    );
  async function logout() {
    await fetch("/api/session", {
      method: "DELETE",
      credentials: "same-origin",
    });
    client.clear();
    window.location.assign("/");
  }
  const workspaceControls = (
    <div className="scope-panel">
      <form
        onSubmit={(e) => {
          e.preventDefault();
          setChosen(tenant.trim());
          setMoy("");
          setOzon("");
        }}
      >
        <label>
          Рабочее пространство
          <input
            value={tenant}
            onChange={(e) => setTenant(e.target.value)}
            placeholder="Идентификатор пространства"
          />
        </label>
        <button className="secondary">Открыть пространство</button>
      </form>
      {accounts.isLoading && <span>Загружаем аккаунты…</span>}
      {accounts.error && <Failure error={accounts.error} />}{" "}
      {accounts.data && (
        <>
          <label>
            МойСклад
            <select value={moy} onChange={(e) => setMoy(e.target.value)}>
              <option value="">Выберите аккаунт</option>
              {accounts.data.accounts
                .filter((a) => a.source === "moysklad")
                .map((a, index) => (
                  <option key={a.account} value={a.account}>
                    {a.name || `Аккаунт ${index + 1}`} ·{" "}
                    {new Date(a.registered_at).toLocaleDateString("ru-RU")}
                  </option>
                ))}
            </select>
          </label>
          <label>
            Ozon
            <select value={ozon} onChange={(e) => setOzon(e.target.value)}>
              <option value="">Выберите аккаунт</option>
              {accounts.data.accounts
                .filter((a) => a.source === "ozon")
                .map((a, index) => (
                  <option key={a.account} value={a.account}>
                    {a.name || `Аккаунт ${index + 1}`} ·{" "}
                    {new Date(a.registered_at).toLocaleDateString("ru-RU")}
                  </option>
                ))}
            </select>
          </label>
          {accounts.data.accounts.length === 0 && (
            <span>В этом пространстве пока нет аккаунтов.</span>
          )}
          {accounts.data.truncated && (
            <span>
              Показана часть аккаунтов. Уточните нужный аккаунт у
              администратора.
            </span>
          )}
        </>
      )}
    </div>
  );
  const importNotice = (
    <ImportSubmissionNotice
      submission={importSubmission}
      scope={{
        tenant: chosen,
        provider:
          importSubmission.pending?.intent.scope.provider ??
          (importSubmission.result?.operation.provider === "moysklad"
            ? "moysklad"
            : "ozon"),
        account:
          (importSubmission.pending?.intent.scope.provider ??
            importSubmission.result?.operation.provider) === "moysklad"
            ? moy
            : ozon,
      }}
      restore={(original) => {
        setTenant(original.tenant);
        setChosen(original.tenant);
        if (original.provider === "moysklad") setMoy(original.account);
        else setOzon(original.account);
        navigate("/integrations");
      }}
    />
  );
  return (
    <div className="shell reference-shell">
      <header className="app-topbar">
        <details
          className="workspace-selector"
          key={chosen || "empty"}
          open={!chosen}
        >
          <summary>
            Рабочее пространство <span aria-hidden="true">⌄</span>
          </summary>
          <div className="workspace-popover">
            {workspaceControls}
            <div className="workspace-footer">
              <NavLink to="/diagnostics">Диагностика</NavLink>
              <button
                className="text-action"
                disabled={
                  (!!confirmation.pending && !confirmation.sessionLost) ||
                  (!!importSubmission.pending &&
                    !importSubmission.sessionLost) ||
                  (!!credentialSubmission.pending &&
                    !credentialSubmission.sessionLost) ||
                  !!stockSyncSubmission.pending
                }
                onClick={() => void logout()}
              >
                Выйти
              </button>
            </div>
          </div>
        </details>
        <nav className="app-tabs" aria-label="Разделы">
          <NavLink to="/" end>
            Обзор
          </NavLink>
          <NavLink to="/products">Товары</NavLink>
          <NavLink to="/warehouses">Склады</NavLink>
          <NavLink to="/integrations">Интеграции</NavLink>
        </nav>
      </header>
      <main className="main">
        <div className="content">
          <ConfirmationNotice
            confirmation={confirmation}
            scope={scope}
            restore={(original) => {
              setTenant(original.tenant);
              setChosen(original.tenant);
              setMoy(original.moy);
              setOzon(original.ozon);
            }}
          />
          <StockSyncNotice
            submission={stockSyncSubmission}
            scope={{
              tenant: chosen,
              moysklad_account: moy,
              ozon_account: ozon,
            }}
            onIntegrations={integrationPage}
            restore={(original) => {
              setTenant(original.tenant);
              setChosen(original.tenant);
              setMoy(original.moysklad_account);
              setOzon(original.ozon_account);
              navigate("/integrations");
            }}
          />
          <CredentialNotice
            submission={credentialSubmission}
            tenant={chosen}
            restore={(original) => {
              setTenant(original.tenant);
              setChosen(original.tenant);
              if (original.provider === "moysklad") setMoy(original.account);
              else setOzon(original.account);
              navigate("/integrations");
            }}
          />
          {!integrationPage && importNotice}
          <Routes>
            <Route
              path="/integrations"
              element={
                <Integrations
                  key={chosen}
                  tenant={chosen}
                  accounts={accounts.data?.accounts ?? []}
                  selected={{ moy, ozon }}
                  onSelect={(provider, account) => {
                    if (provider === "moysklad") setMoy(account);
                    else setOzon(account);
                  }}
                  report={report.data}
                  submission={importSubmission}
                  credentialSubmission={credentialSubmission}
                  stockSyncSubmission={stockSyncSubmission}
                  notice={importNotice}
                />
              }
            />
            <Route
              path="/"
              element={
                <Overview
                  report={report.data}
                  loading={report.isLoading && !!chosen}
                  error={report.error}
                />
              }
            />
            <Route
              path="/products"
              element={
                <Products
                  key={`${scope.tenant}:${scope.moy}:${scope.ozon}`}
                  scope={scope}
                  confirmation={confirmation}
                />
              }
            />
            <Route
              path="/warehouses"
              element={
                <Warehouses
                  key={`${scope.tenant}:${scope.moy}:${scope.ozon}`}
                  scope={scope}
                  confirmation={confirmation}
                />
              }
            />
            <Route
              path="/diagnostics"
              element={
                <Diagnostics
                  report={report.data}
                  loading={report.isLoading && !!chosen}
                  error={report.error}
                />
              }
            />
          </Routes>
        </div>
      </main>
    </div>
  );
}
