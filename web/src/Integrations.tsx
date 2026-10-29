import { useCallback, useRef, useState, type ReactNode } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { ImportPanel } from "./ImportPanel";
import { IntegrationDialog } from "./IntegrationDialog";
import { IntegrationEvents } from "./IntegrationEvents";
import { SupportDiagnostics } from "./SupportDiagnostics";
import { CredentialEditor } from "./CredentialEditor";
import { StockShadow } from "./StockShadow";
import {
  useCredentialSubmission,
  type CredentialSubmissionState,
} from "./CredentialSubmission";
import {
  StockSyncControls,
  type StockSyncSubmissionState,
} from "./StockSyncControls";
import {
  useImportSubmission,
  type ImportSubmissionState,
} from "./ImportSubmission";
import {
  api,
  post,
  params,
  ApiError,
  type Account,
  type Batch,
  type Report,
  type ImportProvider,
  type ImportScope,
  type ImportOperation,
} from "./api";

function accountLabel(
  provider: ImportProvider,
  accounts: Account[],
  selected?: string,
) {
  const entries = accounts.filter((account) => account.source === provider);
  const index = entries.findIndex((account) => account.account === selected);
  return index < 0
    ? "Не выбран"
    : entries[index].name || `Аккаунт ${index + 1}`;
}

type Provider = {
  provider: ImportProvider;
  account?: string;
  name?: string;
  storage_available?: boolean;
  credentials_stored?: boolean;
  credential_revision?: number;
  status:
    | "NOT_CONFIGURED"
    | "CONFIGURED_NOT_VERIFIED"
    | "VERIFIED"
    | "VERIFICATION_FAILED";
  registration:
    "NOT_CHECKED" | "NOT_REGISTERED" | "REGISTERED" | "OWNERSHIP_CONFLICT";
  check?: { checked_at: string; identity: string; reason?: string };
  import?: Batch;
};
const names = { moysklad: "МойСклад", ozon: "Ozon" };
const date = (value: string) =>
  new Date(value).toLocaleString("ru-RU", {
    day: "numeric",
    month: "short",
    hour: "2-digit",
    minute: "2-digit",
  });

export function Integrations({
  tenant,
  accounts,
  selected,
  report,
  submission,
  onSelect,
  notice,
  credentialSubmission,
  stockSyncSubmission,
}: {
  tenant: string;
  accounts: Account[];
  selected?: { moy: string; ozon: string };
  report?: Report;
  submission?: ImportSubmissionState;
  onSelect?: (provider: ImportProvider, account: string) => void;
  notice?: ReactNode;
  credentialSubmission?: CredentialSubmissionState;
  stockSyncSubmission?: StockSyncSubmissionState;
}) {
  const localSubmission = useImportSubmission();
  const imports = submission ?? localSubmission;
  const localCredentials = useCredentialSubmission();
  const credentials = credentialSubmission ?? localCredentials;
  const client = useQueryClient();
  const [busy, setBusy] = useState("");
  const requestLock = useRef(false);
  const [acks, setAcks] = useState<Record<string, boolean>>({});
  const [message, setMessage] = useState("");
  const [autoProgress, setAutoProgress] = useState({
    scope: "",
    enabled: false,
    checked: false,
  });
  const progressScope = `${tenant}:${selected?.moy || ""}:${selected?.ozon || ""}`;
  const onAutomaticProgress = useCallback(
    (enabled: boolean, checked: boolean) => {
      setAutoProgress((old) =>
        old.scope === progressScope &&
        old.enabled === enabled &&
        old.checked === checked
          ? old
          : { scope: progressScope, enabled, checked },
      );
    },
    [progressScope],
  );
  const [creatingProvider, setCreatingProvider] =
    useState<ImportProvider | null>(null);
  const [historyOpen, setHistoryOpen] = useState(false);
  const [diagnosticsOpen, setDiagnosticsOpen] = useState(false);
  const [diagnosticOperation, setDiagnosticOperation] = useState<
    ImportOperation | undefined
  >(undefined);
  const [settingsProvider, setSettingsProvider] =
    useState<ImportProvider | null>(null);
  const [uncertain, setUncertain] = useState("");
  const [historyTarget, setHistoryTarget] = useState<HTMLElement | null>(null);
  const [operationStates, setOperationStates] = useState<
    Partial<
      Record<
        ImportProvider,
        { account: string; state?: ImportOperation["state"] }
      >
    >
  >({});
  const onOperationState = useCallback(
    (scope: ImportScope, state: ImportOperation["state"] | undefined) => {
      setOperationStates((old) =>
        old[scope.provider]?.account === scope.account &&
        old[scope.provider]?.state === state
          ? old
          : { ...old, [scope.provider]: { account: scope.account, state } },
      );
    },
    [],
  );
  const query = useQuery({
    queryKey: [
      "integrations",
      tenant,
      selected?.moy || "",
      selected?.ozon || "",
    ],
    queryFn: ({ signal }) =>
      api<{ providers: Provider[] }>(
        "/api/integrations?" +
          params({
            tenant,
            moysklad_account: selected?.moy || "",
            ozon_account: selected?.ozon || "",
          }),
        signal,
      ),
    enabled: !!tenant,
    retry: false,
  });
  const eligibility = useQuery({
    queryKey: [
      "publication-eligibility",
      tenant,
      selected?.moy,
      selected?.ozon,
    ],
    queryFn: ({ signal }) =>
      api<{ state: string }>(
        "/api/stock-sync/eligibility?" +
          params({
            tenant,
            moysklad_account: selected?.moy || "",
            ozon_account: selected?.ozon || "",
          }),
        signal,
      ),
    enabled: !!tenant && !!selected?.moy && !!selected?.ozon,
    retry: false,
  });
  function openSettings(provider: ImportProvider) {
    if (!uncertain) setMessage("");
    setSettingsProvider(provider);
  }
  async function probe(provider: string) {
    if (requestLock.current) return;
    requestLock.current = true;
    setBusy(provider);
    setMessage("");
    try {
      await post(`/api/integrations/${provider}/probe`, {
        tenant,
        account:
          (provider === "moysklad" ? selected?.moy : selected?.ozon) ||
          query.data?.providers.find((p) => p.provider === provider)?.account ||
          "",
      });
      await query.refetch();
    } catch (error) {
      setMessage(
        error instanceof ApiError && error.status === 401
          ? "Сессия завершилась. Войдите снова."
          : "Не удалось проверить подключение. Попробуйте ещё раз. Загрузка данных не запускалась.",
      );
      if (error instanceof ApiError && error.status === 401)
        void client.invalidateQueries({ queryKey: ["session"] });
    } finally {
      requestLock.current = false;
      setBusy("");
    }
  }
  async function reconcile(provider: string) {
    if (requestLock.current) return;
    requestLock.current = true;
    setBusy(provider);
    try {
      const result = await query.refetch();
      if (result.isError || !result.data) throw new Error("status unavailable");
      setUncertain("");
      setMessage(
        "Статус добавления проверен. Проверьте аккаунт перед следующим действием.",
      );
      void client.invalidateQueries({ queryKey: ["accounts", tenant] });
    } catch {
      setMessage(
        "Результат добавления пока неизвестен. Проверьте статус перед повторной попыткой.",
      );
    } finally {
      requestLock.current = false;
      setBusy("");
    }
  }
  async function register(provider: string) {
    if (requestLock.current || uncertain || !acks[provider]) return;
    requestLock.current = true;
    setBusy(provider);
    setMessage("");
    try {
      await post(`/api/integrations/${provider}/register`, {
        tenant,
        acknowledge: true,
      });
      setAcks((old) => ({ ...old, [provider]: false }));
      setMessage(
        "Аккаунт добавлен. Предыдущие загрузки не подключаются автоматически.",
      );
      await Promise.all([
        query.refetch(),
        client.invalidateQueries({ queryKey: ["accounts", tenant] }),
        client.invalidateQueries({ queryKey: ["diagnostics"] }),
      ]);
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) {
        setUncertain(provider);
        setMessage(
          "Сессия завершилась; аккаунт мог быть добавлен. Войдите снова и проверьте статус.",
        );
        void client.invalidateQueries({ queryKey: ["session"] });
      } else if (
        error instanceof ApiError &&
        error.code === "OWNERSHIP_CONFLICT"
      ) {
        setMessage(
          "Этот аккаунт уже подключён к другой учётной записи STYK. Перенос недоступен.",
        );
        void query.refetch();
      } else if (
        error instanceof ApiError &&
        [
          "PROBE_REQUIRED",
          "INVALID_REQUEST",
          "NOT_CONFIGURED",
          "FORBIDDEN",
        ].includes(error.code || "")
      ) {
        setMessage(
          "Аккаунт не добавлен. Проверьте настройки, подключение и подтверждение принадлежности.",
        );
      } else {
        setUncertain(provider);
        setMessage(
          "Результат добавления неизвестен. Проверьте статус аккаунта перед повторной попыткой.",
        );
      }
    } finally {
      requestLock.current = false;
      setBusy("");
    }
  }
  const selectedAccount = (provider: ImportProvider) =>
    provider === "moysklad" ? selected?.moy : selected?.ozon;
  const providers = query.data?.providers || [];
  function batchFor(provider: ImportProvider) {
    const account = selectedAccount(provider);
    if (
      !account ||
      !accounts.some((a) => a.source === provider && a.account === account)
    )
      return undefined;
    const configured = providers.find((p) => p.provider === provider);
    return (
      report?.batches[provider] ??
      (configured?.account === account ? configured.import : undefined)
    );
  }
  const eligible = (provider: ImportProvider) =>
    !!(batchFor(provider)?.present && batchFor(provider)?.completed);
  const bothReady = eligible("moysklad") && eligible("ozon");
  const syncEnabled =
    autoProgress.scope === progressScope && autoProgress.enabled;
  const firstCheckCompleted = syncEnabled && autoProgress.checked;
  const connected = (["moysklad", "ozon"] as const).filter((provider) => {
    const p = providers.find((item) => item.provider === provider);
    return (
      !!selectedAccount(provider) &&
      p?.account === selectedAccount(provider) &&
      p?.registration === "REGISTERED" &&
      p?.status === "VERIFIED"
    );
  });
  const activeProvider = (["moysklad", "ozon"] as const).find(
    (provider) =>
      operationStates[provider]?.account === selectedAccount(provider) &&
      ["QUEUED", "RUNNING", "OUTCOME_UNKNOWN"].includes(
        operationStates[provider]?.state || "",
      ),
  );
  const mapped = report?.mapping_counts.catalog_confirmed_products;
  const catalogTotal = report?.mapping_counts.catalog_products;
  const productsMapped = bothReady && typeof mapped === "number" && mapped > 0;
  const warehousesMapped =
    (report?.mapping_counts.confirmed_warehouses || 0) > 0;
  const setupStep =
    connected.length < 2
      ? 1
      : !bothReady
        ? 2
        : !productsMapped || !warehousesMapped
          ? 3
          : !syncEnabled
            ? 4
            : 5;
  const settingsFeedback = (
    <>
      {message && (
        <p className="integration-feedback" role="alert">
          {message}
        </p>
      )}
      {uncertain && (
        <button
          className="secondary"
          disabled={!!busy}
          onClick={() => void reconcile(uncertain)}
        >
          Проверить результат добавления
        </button>
      )}
    </>
  );
  const historyAccounts = Object.fromEntries(
    (["moysklad", "ozon"] as const)
      .filter((provider) =>
        accounts.some(
          (a) =>
            a.source === provider && a.account === selectedAccount(provider),
        ),
      )
      .map((provider) => [provider, selectedAccount(provider)]),
  );
  if (!tenant)
    return (
      <div className="notice">
        Войдите в аккаунт STYK, чтобы подключить сервисы.
      </div>
    );
  return (
    <div className="integration-console">
      <div className="integrations-heading">
        <h1>Интеграции</h1>
        <p>
          Свяжите МойСклад и Ozon, чтобы управлять загрузкой данных и отправкой
          остатков.
        </p>
      </div>
      {eligibility.data?.state && (
        <p className="publication-eligibility" role="status">
          {eligibility.data.state === "CONTROLLED_TEST_AVAILABLE"
            ? "Тестовая публикация разрешена для этой пары аккаунтов."
            : eligibility.data.state === "CONTROLLED_TEST_DISABLED"
              ? "Тестовая публикация выключена. Чтение данных доступно."
              : "Доступно чтение данных. Публикация остатков ожидает отдельной активации."}
        </p>
      )}
      {notice}
      {!settingsProvider && settingsFeedback}
      {query.isLoading && <p role="status">Загружаем интеграции…</p>}
      {query.error && (
        <p className="integration-feedback" role="alert">
          Не удалось получить состояние интеграций.{" "}
          <button className="text-action" onClick={() => void query.refetch()}>
            Повторить
          </button>
        </p>
      )}
      <section className="setup-card" aria-label="Настройка синхронизации">
        <div className="setup-heading">
          <h2>Настройка синхронизации</h2>
          <span>
            {connected.length === 2 &&
            bothReady &&
            firstCheckCompleted &&
            productsMapped &&
            warehousesMapped
              ? "Все шаги выполнены"
              : `Шаг ${setupStep} из 5`}
          </span>
        </div>
        <ol className="setup-steps">
          <li
            className={`setup-step ${connected.length === 2 ? "" : "pending"}`}
          >
            <span
              className={`step-symbol ${connected.length === 2 ? "done" : "warning"}`}
              aria-hidden="true"
            >
              {connected.length === 2 ? "✓" : "!"}
            </span>
            <div>
              <strong>Подключить магазины</strong>
              <p>
                {connected.length === 2
                  ? "МойСклад и Ozon"
                  : `Проверено подключений: ${connected.length} из 2`}
              </p>
            </div>
          </li>
          <li className={`setup-step ${setupStep < 2 ? "pending" : ""}`}>
            <span
              className={`step-symbol ${bothReady ? "done" : setupStep === 2 ? "current" : ""}`}
              aria-hidden="true"
            >
              {bothReady ? "✓" : "2"}
            </span>
            <div>
              <strong>Загрузить данные</strong>
              <p>
                {activeProvider
                  ? `Идёт загрузка ${names[activeProvider]}`
                  : bothReady
                    ? "Данные обеих систем загружены"
                    : "Товары, склады и остатки"}
              </p>
            </div>
          </li>
          <li className={`setup-step ${bothReady ? "" : "pending"}`}>
            <span
              className={`step-symbol ${productsMapped && warehousesMapped ? "done" : setupStep === 3 ? "current" : ""}`}
              aria-hidden="true"
            >
              {productsMapped && warehousesMapped ? "✓" : "3"}
            </span>
            <div>
              <strong>
                {bothReady ? (
                  <Link to="/products">Сопоставить товары</Link>
                ) : (
                  "Сопоставить товары"
                )}
              </strong>
              <p>
                {!bothReady
                  ? "Откроется после загрузки данных"
                  : typeof mapped === "number" &&
                      typeof catalogTotal === "number"
                    ? `${mapped} из ${catalogTotal} · остальные товары можно настроить позже`
                    : "Проверьте соответствия товаров и складов"}
              </p>
              {bothReady && (
                <Link to="/warehouses" className="step-link">
                  Сопоставить склады
                </Link>
              )}
            </div>
          </li>
          <li className={`setup-step ${setupStep < 4 ? "pending" : ""}`}>
            <span
              className={`step-symbol ${syncEnabled ? "done" : setupStep === 4 ? "current" : ""}`}
              aria-hidden="true"
            >
              {syncEnabled ? "✓" : "4"}
            </span>
            <div>
              <strong>Настроить и включить</strong>
              <p>
                {syncEnabled ? "Включена" : "Нужна проверка перед включением"}
              </p>
            </div>
          </li>
          <li className={`setup-step ${setupStep < 5 ? "pending" : ""}`}>
            <span
              className={`step-symbol ${firstCheckCompleted ? "done" : setupStep === 5 ? "current" : ""}`}
              aria-hidden="true"
            >
              {firstCheckCompleted ? "✓" : "5"}
            </span>
            <div>
              <strong>Проверить результат</strong>
              <p>
                {firstCheckCompleted
                  ? "Первая проверка выбранных товаров завершена"
                  : "После первой проверки покажем результат"}
              </p>
            </div>
          </li>
        </ol>
      </section>
      <StockSyncControls
        tenant={tenant}
        moy={selected?.moy || ""}
        ozon={selected?.ozon || ""}
        eligible={bothReady && productsMapped && warehousesMapped}
        submission={stockSyncSubmission}
        onAutomaticProgress={onAutomaticProgress}
      />
      <StockShadow
        tenant={tenant}
        moy={selected?.moy || ""}
        ozon={selected?.ozon || ""}
        moyLabel={accountLabel("moysklad", accounts, selected?.moy)}
        ozonLabel={accountLabel("ozon", accounts, selected?.ozon)}
      />
      <div className="integration-cards">
        {providers.map((p) => {
          const registeredAccounts = accounts.filter(
            (a) => a.source === p.provider,
          );
          const account = selectedAccount(p.provider);
          const registered =
            !!account && registeredAccounts.some((a) => a.account === account);
          const configured =
            p.account === account && p.status !== "NOT_CONFIGURED";
          const state =
            operationStates[p.provider]?.account === account
              ? operationStates[p.provider]?.state
              : undefined;
          const running =
            state === "RUNNING" ||
            state === "QUEUED" ||
            state === "OUTCOME_UNKNOWN";
          const connectionFailed =
            p.status === "VERIFICATION_FAILED" ||
            p.registration === "OWNERSHIP_CONFLICT";
          const needsAction = connectionFailed || state === "FAILED";
          const checkAge = p.check
            ? Date.now() - Date.parse(p.check.checked_at)
            : NaN;
          const recentProbe =
            p.status === "VERIFIED" &&
            Number.isFinite(checkAge) &&
            checkAge >= 0 &&
            checkAge <= 5 * 60 * 1000;
          const status =
            state === "OUTCOME_UNKNOWN"
              ? "Результат уточняется"
              : state === "RUNNING"
                ? "Загружаем…"
                : state === "QUEUED"
                  ? "В очереди"
                  : needsAction
                    ? "Нужно действие"
                    : registered && configured && p.status === "VERIFIED"
                      ? "Работает"
                      : registered
                        ? "Доступ не проверен"
                        : "Не подключено";
          const problem =
            p.registration === "OWNERSHIP_CONFLICT"
              ? "Аккаунт подключён к другой учётной записи STYK. Выберите другой аккаунт."
              : p.status === "VERIFICATION_FAILED"
                ? p.check?.reason === "INVALID_CREDENTIALS"
                  ? `${names[p.provider]} не принял ключ доступа. Возможно, он истёк или был отозван.`
                  : "Не удалось проверить доступ к сервису. Проверьте настройки и повторите проверку."
                : undefined;
          return (
            <article
              className={`integration-card ${needsAction ? "needs-action" : ""}`}
              key={p.provider}
              aria-label={names[p.provider]}
            >
              <header className="provider-heading">
                <h2>{names[p.provider]}</h2>
                <span
                  className={`connection-state ${state === "OUTCOME_UNKNOWN" ? "warning" : running ? "active" : needsAction ? "warning" : p.status === "VERIFIED" && registered ? "verified" : ""}`}
                >
                  <span aria-hidden="true">
                    {state === "OUTCOME_UNKNOWN"
                      ? "?"
                      : running
                        ? "↻"
                        : needsAction
                          ? "△"
                          : p.status === "VERIFIED" && registered
                            ? "✓"
                            : ""}
                  </span>
                  {status}
                </span>
              </header>
              {registered ? (
                <ImportPanel
                  key={`${tenant}:${p.provider}:${account}`}
                  scope={{ tenant, provider: p.provider, account: account! }}
                  submission={imports}
                  latestBatch={batchFor(p.provider)}
                  configured={configured}
                  historyTarget={historyTarget}
                  historyManaged
                  onSettings={() => openSettings(p.provider)}
                  onProbe={() => void probe(p.provider)}
                  probeBusy={!!busy}
                  connectionProblem={problem}
                  repairCredentials={
                    p.status === "VERIFICATION_FAILED" &&
                    p.check?.reason === "INVALID_CREDENTIALS"
                  }
                  accountLabel={
                    registeredAccounts.find((a) => a.account === account)
                      ?.name ||
                    `Аккаунт ${registeredAccounts.findIndex((a) => a.account === account) + 1}`
                  }
                  onStateChange={onOperationState}
                />
              ) : (
                <div className="provider-empty">
                  <p>
                    {problem ||
                      (registeredAccounts.length
                        ? "Выберите подключение, чтобы загрузить данные и увидеть его историю."
                        : `Подключите ${names[p.provider]}, чтобы загрузить товары, склады и остатки.`)}
                  </p>
                  <div className="provider-actions">
                    <button onClick={() => openSettings(p.provider)}>
                      {registeredAccounts.length
                        ? "Выбрать аккаунт"
                        : "Подключить"}
                    </button>
                    {p.status !== "NOT_CONFIGURED" && (
                      <button
                        className="text-action"
                        disabled={!!busy}
                        onClick={() => void probe(p.provider)}
                      >
                        Проверить доступ
                      </button>
                    )}
                  </div>
                  <p className="provider-footnote">
                    Мы только читаем данные. Загрузка не меняет товары и остатки
                    в сервисе.
                  </p>
                </div>
              )}
              {settingsProvider === p.provider && (
                <IntegrationDialog
                  title={`Подключение ${names[p.provider]}`}
                  closeLabel="Закрыть настройки"
                  onClose={() => setSettingsProvider(null)}
                >
                  <div className="provider-settings-body">
                    {settingsFeedback}
                    <CredentialEditor
                      key={`${tenant}:${p.provider}:${creatingProvider === p.provider ? "new" : account || p.account || "new"}`}
                      scope={{
                        tenant,
                        provider: p.provider,
                        account:
                          creatingProvider === p.provider
                            ? ""
                            : account || p.account || "",
                      }}
                      name={p.name}
                      registration={p.status}
                      reason={p.check?.reason}
                      accounts={registeredAccounts}
                      onSelect={(a) => {
                        setCreatingProvider(a ? null : p.provider);
                        onSelect?.(p.provider, a);
                      }}
                      submission={credentials}
                      onClose={() => setSettingsProvider(null)}
                    />
                    <details className="legacy-connection-settings">
                      <summary>Проверка доступа и настройки сервера</summary>
                      <div className="provider-settings-body">
                        <p>
                          {p.status === "VERIFIED" && p.check
                            ? `Доступ проверен ${date(p.check.checked_at)}.`
                            : "Доступ пока не подтверждён."}{" "}
                          Проверка подключения не запускает загрузку каталога.
                          Проверка доступа не заменяет подтверждение
                          принадлежности аккаунта.
                        </p>
                        <button
                          className="secondary"
                          disabled={!!busy || p.status === "NOT_CONFIGURED"}
                          onClick={() => void probe(p.provider)}
                        >
                          {busy === p.provider
                            ? "Подождите…"
                            : "Проверить подключение"}
                        </button>
                        {p.registration === "NOT_REGISTERED" && (
                          <div className="registration-form">
                            <p>
                              Добавьте настроенный на сервере аккаунт в STYK.
                              Предыдущие загрузки не будут
                              включены автоматически.
                            </p>
                            {p.status === "VERIFIED" && !recentProbe && (
                              <p>
                                Перед добавлением аккаунта повторите проверку:
                                прошло больше пяти минут.
                              </p>
                            )}
                            <label className="integration-ack">
                              <input
                                type="checkbox"
                                checked={!!acks[p.provider]}
                                onChange={(e) =>
                                  setAcks((old) => ({
                                    ...old,
                                    [p.provider]: e.target.checked,
                                  }))
                                }
                              />
                              Подтверждаю, что этот аккаунт принадлежит мне.
                            </label>
                            <button
                              disabled={
                                !!busy ||
                                !!uncertain ||
                                !acks[p.provider] ||
                                !recentProbe
                              }
                              onClick={() => void register(p.provider)}
                            >
                              Добавить аккаунт
                            </button>
                          </div>
                        )}
                        <details className="technical-details">
                          <summary>Технические детали</summary>
                          <dl>
                            <div>
                              <dt>Настроенный аккаунт</dt>
                              <dd>
                                <code>{p.account || "Не задан"}</code>
                              </dd>
                            </div>
                            {p.check && (
                              <div>
                                <dt>Результат проверки</dt>
                                <dd>
                                  <code>
                                    {p.check.reason || p.check.identity}
                                  </code>
                                </dd>
                              </div>
                            )}
                          </dl>
                          <p>
                            Проверка чтения данных не подтверждает готовность
                            публикации остатков.
                          </p>
                        </details>
                        <nav aria-label={`${names[p.provider]}: разделы`}>
                          <Link to="/products">Товары</Link>
                          <Link to="/warehouses">Склады</Link>
                          <Link to="/diagnostics">Диагностика</Link>
                        </nav>
                      </div>
                    </details>
                  </div>
                </IntegrationDialog>
              )}
            </article>
          );
        })}
      </div>
      <IntegrationEvents
        tenant={tenant}
        accounts={historyAccounts}
        providers={providers}
        onSupport={(operation) => {
          setDiagnosticOperation(operation);
          setDiagnosticsOpen(true);
        }}
        onRepair={openSettings}
        onHistory={() => setHistoryOpen(true)}
      />
      {historyOpen && (
        <IntegrationDialog
          title="История обновлений"
          onClose={() => setHistoryOpen(false)}
        >
          <div ref={setHistoryTarget} />
          {!selected?.moy && !selected?.ozon && (
            <p>Выберите аккаунт в настройках подключения.</p>
          )}
        </IntegrationDialog>
      )}
      {diagnosticsOpen && (
        <SupportDiagnostics
          operation={diagnosticOperation}
          onClose={() => setDiagnosticsOpen(false)}
        />
      )}
    </div>
  );
}
