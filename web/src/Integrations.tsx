import { useCallback, useRef, useState, type ReactNode } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { ImportPanel } from "./ImportPanel";
import { IntegrationDialog } from "./IntegrationDialog";
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

type Provider = {
  provider: ImportProvider;
  account?: string;
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
}: {
  tenant: string;
  accounts: Account[];
  selected?: { moy: string; ozon: string };
  report?: Report;
  submission?: ImportSubmissionState;
  onSelect?: (provider: ImportProvider, account: string) => void;
  notice?: ReactNode;
}) {
  const localSubmission = useImportSubmission();
  const imports = submission ?? localSubmission;
  const client = useQueryClient();
  const [busy, setBusy] = useState("");
  const requestLock = useRef(false);
  const [acks, setAcks] = useState<Record<string, boolean>>({});
  const [message, setMessage] = useState("");
  const [copiedProvider, setCopiedProvider] = useState("");
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
  const settings = useRef<
    Partial<Record<ImportProvider, HTMLDetailsElement | null>>
  >({});
  const query = useQuery({
    queryKey: ["integrations", tenant],
    queryFn: ({ signal }) =>
      api<{ providers: Provider[] }>(
        "/api/integrations?" + params({ tenant }),
        signal,
      ),
    enabled: !!tenant,
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
      await post(`/api/integrations/${provider}/probe`, {});
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
          "Этот аккаунт уже добавлен в другое рабочее пространство. Перенос недоступен.",
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
  const eligible = (provider: ImportProvider) => {
    const account = selectedAccount(provider);
    const batch =
      account &&
      accounts.some((a) => a.source === provider && a.account === account)
        ? report?.batches[provider]
        : undefined;
    return !!(batch?.present && batch.completed);
  };
  const bothReady = eligible("moysklad") && eligible("ozon");
  const missing = (["moysklad", "ozon"] as const).find(
    (provider) => !eligible(provider),
  );
  const missingProvider = query.data?.providers.find(
    (p) => p.provider === missing,
  );
  const missingAccounts = accounts.filter((a) => a.source === missing);
  const missingState =
    missing && operationStates[missing]?.account === selectedAccount(missing)
      ? operationStates[missing]?.state
      : undefined;
  const missingActive =
    !!missingState &&
    ["QUEUED", "RUNNING", "OUTCOME_UNKNOWN"].includes(missingState);
  const canLoadMissing =
    !!missing &&
    !!selectedAccount(missing) &&
    missingAccounts.some((a) => a.account === selectedAccount(missing)) &&
    missingProvider?.account === selectedAccount(missing) &&
    missingProvider?.status !== "NOT_CONFIGURED";
  const nextText = missingActive
    ? missingState === "OUTCOME_UNKNOWN"
      ? "Дождитесь уточнения результата"
      : "Дождитесь завершения загрузки"
    : !missingProvider || missingProvider.status === "NOT_CONFIGURED"
      ? `Настройте подключение ${missing ? names[missing] : ""}`
      : missingProvider.status === "VERIFICATION_FAILED"
        ? `Проверьте доступ к ${missing ? names[missing] : ""}`
        : missingAccounts.length === 0
          ? `Добавьте аккаунт ${missing ? names[missing] : ""}`
          : !selectedAccount(missing!)
            ? `Выберите аккаунт ${names[missing!]}`
            : "Загрузите первый каталог";
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
  if (!tenant)
    return (
      <div className="notice">
        Выберите рабочее пространство, чтобы настроить интеграции.
      </div>
    );
  return (
    <div className="integration-console">
      <div className="integrations-heading">
        <h1>Интеграции</h1>
        <p>Подключайте сервисы и обновляйте данные в одном месте.</p>
      </div>
      {notice}
      {!settingsProvider && settingsFeedback}
      {query.isLoading && <p role="status">Загружаем интеграции…</p>}
      {query.error && (
        <p className="integration-feedback is-error" role="alert">
          Не удалось получить состояние интеграций.{" "}
          <button className="text-button" onClick={() => void query.refetch()}>
            Повторить
          </button>
        </p>
      )}
      <div className="integration-cards">
        {query.data?.providers.map((p) => {
          const registeredAccounts = accounts.filter(
            (a) => a.source === p.provider,
          );
          const account = selectedAccount(p.provider);
          const registered =
            !!account && registeredAccounts.some((a) => a.account === account);
          const configured =
            p.account === account && p.status !== "NOT_CONFIGURED";
          const checkAge = p.check
            ? Date.now() - Date.parse(p.check.checked_at)
            : NaN;
          const recentProbe =
            p.status === "VERIFIED" &&
            Number.isFinite(checkAge) &&
            checkAge >= 0 &&
            checkAge <= 5 * 60 * 1000;
          const connectionLabel =
            registered && !configured
              ? "Доступ не настроен"
              : p.status === "NOT_CONFIGURED"
                ? "Не настроено"
                : p.registration === "OWNERSHIP_CONFLICT"
                  ? "Аккаунт недоступен"
                  : p.status === "VERIFICATION_FAILED"
                    ? "Ошибка проверки доступа"
                    : registered || p.registration === "REGISTERED"
                      ? "Аккаунт добавлен"
                      : "Требуется подключение";
          const accessLabel =
            p.registration === "OWNERSHIP_CONFLICT"
              ? "Аккаунт принадлежит другому рабочему пространству."
              : registered && !configured
                ? "Выбран другой аккаунт. Загрузка недоступна."
                : p.status === "VERIFIED" && p.check
                  ? `Доступ проверен ${date(p.check.checked_at)}`
                  : p.status === "VERIFICATION_FAILED"
                    ? "Проверьте настройки и повторите проверку."
                    : p.status === "NOT_CONFIGURED"
                      ? "Добавьте данные доступа в настройках сервиса."
                      : "Данные доступа настроены · доступ не проверен";
          return (
            <article
              className="integration-card"
              key={p.provider}
              aria-label={names[p.provider]}
            >
              <header className="provider-heading">
                <span
                  className={`provider-mark provider-${p.provider}`}
                  aria-hidden="true"
                >
                  {p.provider === "moysklad" ? (
                    <svg viewBox="0 0 32 32" fill="none">
                      <path
                        d="m5 10 11-6 11 6-11 6-11-6Zm0 6 11 6 11-6M5 22l11 6 11-6"
                        stroke="currentColor"
                        strokeWidth="2.2"
                        strokeLinejoin="round"
                      />
                    </svg>
                  ) : (
                    "O"
                  )}
                </span>
                <div>
                  <h2>{names[p.provider]}</h2>
                  <p
                    className={`connection-state ${p.status === "VERIFICATION_FAILED" || p.registration === "OWNERSHIP_CONFLICT" ? "is-error" : registered && configured ? "is-connected" : ""}`}
                  >
                    <span aria-hidden="true" />
                    {connectionLabel}
                  </p>
                </div>
              </header>
              <p className="access-state">{accessLabel}</p>
              {registeredAccounts.length > 1 && registered && (
                <p className="selected-account">
                  Аккаунт{" "}
                  {registeredAccounts.findIndex((a) => a.account === account) +
                    1}{" "}
                  из {registeredAccounts.length}
                </p>
              )}
              {registered ? (
                <ImportPanel
                  key={`${tenant}:${p.provider}:${account}`}
                  scope={{ tenant, provider: p.provider, account: account! }}
                  submission={imports}
                  latestBatch={report?.batches[p.provider]}
                  configured={configured}
                  historyTarget={historyTarget}
                  accountLabel={`Аккаунт ${registeredAccounts.findIndex((a) => a.account === account) + 1}`}
                  onStateChange={onOperationState}
                />
              ) : (
                <div className="provider-empty">
                  <p>
                    {registeredAccounts.length > 0
                      ? "Выберите аккаунт, чтобы увидеть его данные и историю."
                      : "Каталог ещё не загружен"}
                  </p>
                  <button className="primary-button" disabled>
                    Загрузить данные
                  </button>
                </div>
              )}
              <details
                className="provider-settings"
                open={settingsProvider === p.provider}
                ref={(node) => {
                  settings.current[p.provider] = node;
                }}
              >
                <summary
                  aria-haspopup="dialog"
                  onClick={(e) => {
                    e.preventDefault();
                    openSettings(p.provider);
                  }}
                >
                  Настройки
                </summary>
                {settingsProvider === p.provider && (
                  <IntegrationDialog
                    title={`Настройки ${names[p.provider]}`}
                    closeLabel="Закрыть настройки"
                    onClose={() => setSettingsProvider(null)}
                  >
                    <div className="provider-settings-body">
                      {settingsFeedback}
                      {registeredAccounts.length > 0 && (
                        <label className="integration-field">
                          Аккаунт для работы
                          <select
                            aria-label={`${names[p.provider]}: аккаунт`}
                            value={account || ""}
                            disabled={!onSelect}
                            onChange={(e) =>
                              onSelect?.(p.provider, e.target.value)
                            }
                          >
                            <option value="">Выберите аккаунт</option>
                            {registeredAccounts.map((a, i) => (
                              <option key={a.account} value={a.account}>
                                Аккаунт {i + 1}
                                {a.account === p.account
                                  ? " · настроен"
                                  : ""} ·{" "}
                                {new Date(a.registered_at).toLocaleDateString(
                                  "ru-RU",
                                )}
                              </option>
                            ))}
                          </select>
                        </label>
                      )}
                      <p>
                        {p.status === "NOT_CONFIGURED"
                          ? "Данные доступа пока не настроены. Обратитесь к администратору STYK, чтобы добавить их в конфигурацию сервиса."
                          : "Проверка подключения читает данные сервиса и не запускает загрузку каталога."}
                      </p>
                      {p.registration === "NOT_REGISTERED" &&
                        p.status === "VERIFIED" &&
                        !recentProbe && (
                          <p>
                            Перед добавлением аккаунта повторите проверку:
                            прошло больше пяти минут.
                          </p>
                        )}
                      <button
                        className="secondary"
                        disabled={!!busy || p.status === "NOT_CONFIGURED"}
                        onClick={() => void probe(p.provider)}
                      >
                        {busy === p.provider
                          ? "Подождите…"
                          : "Проверить подключение"}
                      </button>
                      {p.check && (
                        <p>
                          Последняя проверка: {date(p.check.checked_at)}.{" "}
                          {p.status === "VERIFICATION_FAILED"
                            ? "Доступ не подтверждён."
                            : "Чтение данных доступно."}{" "}
                          {p.check.identity === "MATCHED_RETURNED_PRODUCT"
                            ? "Полученный товар соответствует настроенному аккаунту."
                            : "Принадлежность аккаунта не подтверждена независимо."}
                        </p>
                      )}
                      {p.registration === "NOT_REGISTERED" && (
                        <div className="registration-form">
                          <p>
                            Добавьте настроенный аккаунт в текущее рабочее
                            пространство. Убедитесь, что выбрали верный аккаунт
                            и пространство. Предыдущие загрузки не будут
                            включены.
                          </p>
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
                            Подтверждаю, что этот аккаунт принадлежит текущему
                            рабочему пространству.
                          </label>
                          <p>
                            Это ваше подтверждение принадлежности аккаунта.
                            Проверка доступа не заменяет его.
                          </p>
                          <button
                            className="primary-button"
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
                      {p.registration === "OWNERSHIP_CONFLICT" && (
                        <p className="is-error" role="alert">
                          Аккаунт уже принадлежит другому рабочему пространству.
                          Перенос недоступен.
                        </p>
                      )}
                      <details className="import-help">
                        <summary>О загрузке данных</summary>
                        <p>
                          Загружаются товары, склады и остатки
                          {p.provider === "moysklad"
                            ? ", включая варианты товаров"
                            : ""}
                          . Данные в сервисе не изменяются. Отсутствие записи об
                          остатках не означает нулевой остаток.
                        </p>
                        <p>
                          Если ответ потерян или страница закрылась, сначала
                          проверьте историю выбранного аккаунта. Не создавайте
                          повторную загрузку, пока не уточните результат.
                        </p>
                      </details>
                      <details className="technical-details">
                        <summary>Технические детали</summary>
                        <dl>
                          <div>
                            <dt>Рабочее пространство</dt>
                            <dd>
                              <code>{tenant}</code>
                            </dd>
                          </div>
                          <div>
                            <dt>Настроенный аккаунт</dt>
                            <dd>
                              <code>{p.account || "Не задан"}</code>
                            </dd>
                          </div>
                          {p.check?.reason && (
                            <div>
                              <dt>Результат проверки доступа</dt>
                              <dd>
                                <code>{p.check.reason}</code>
                              </dd>
                            </div>
                          )}
                          {account && (
                            <div>
                              <dt>Выбранный аккаунт</dt>
                              <dd>
                                <code>{account}</code>
                              </dd>
                            </div>
                          )}
                        </dl>
                        {registeredAccounts.length > 0 && (
                          <>
                            <h4>Добавленные аккаунты</h4>
                            <ul className="technical-account-list">
                              {registeredAccounts.map((a, index) => (
                                <li key={a.account}>
                                  <span>
                                    Аккаунт {index + 1} ·{" "}
                                    {date(a.registered_at)}
                                  </span>
                                  <code>{a.account}</code>
                                </li>
                              ))}
                            </ul>
                          </>
                        )}
                        <button
                          className="secondary"
                          onClick={async () => {
                            try {
                              await navigator.clipboard.writeText(
                                `tenant: ${tenant}\nconfigured account: ${p.account || ""}\nselected account: ${account || ""}\n${registeredAccounts.map((a, i) => `account ${i + 1}: ${a.account}`).join("\n")}`,
                              );
                              setCopiedProvider(p.provider);
                            } catch {
                              setMessage(
                                "Не удалось скопировать. Выделите идентификатор вручную.",
                              );
                            }
                          }}
                        >
                          {copiedProvider === p.provider
                            ? "Скопировано"
                            : "Копировать идентификаторы"}
                        </button>
                        <p>
                          Проверка доступа не подтверждает все разрешения и
                          готовность к публикации остатков.
                        </p>
                      </details>
                      <nav aria-label={`${names[p.provider]}: разделы`}>
                        <Link to="/products">Товары</Link>
                        <Link to="/warehouses">Склады</Link>
                        <Link to="/diagnostics">Диагностика</Link>
                      </nav>
                    </div>
                  </IntegrationDialog>
                )}
              </details>
            </article>
          );
        })}
      </div>
      {query.data && (
        <section
          className={`integration-next ${bothReady ? "is-ready" : ""}`}
          aria-label="Следующий шаг"
        >
          <span className="next-icon" aria-hidden="true">
            {bothReady ? "✓" : "→"}
          </span>
          <div>
            <h2>{bothReady ? "Данные обеих систем загружены" : nextText}</h2>
            <p>
              {bothReady
                ? "Теперь можно проверить соответствия товаров и складов."
                : missingActive
                  ? "Состояние обновляется автоматически. Не запускайте повторную загрузку."
                  : "Подключите обе системы и загрузите данные для проверки соответствий."}
            </p>
            {bothReady && (
              <p className="next-note">
                Соответствия подтверждаются вручную. Публикация остатков
                выключена.
              </p>
            )}
          </div>
          <div className="next-actions">
            {bothReady ? (
              <>
                <Link className="primary-button" to="/products">
                  Сопоставить товары
                </Link>
                <Link className="secondary" to="/warehouses">
                  Сопоставить склады
                </Link>
              </>
            ) : (
              missing &&
              !missingActive && (
                <button
                  className="secondary"
                  onClick={() => {
                    if (
                      canLoadMissing &&
                      missingProvider?.status !== "VERIFICATION_FAILED"
                    ) {
                      const action = settings.current[missing]
                        ?.closest("article")
                        ?.querySelector<HTMLButtonElement>(".import-primary");
                      action?.scrollIntoView?.({ block: "center" });
                      action?.focus();
                    } else openSettings(missing);
                  }}
                >
                  {canLoadMissing &&
                  missingProvider?.status !== "VERIFICATION_FAILED"
                    ? "К загрузке данных"
                    : `Настроить ${names[missing]}`}
                </button>
              )
            )}
          </div>
        </section>
      )}
      <section className="updates-history" aria-label="История обновлений">
        <div className="history-heading">
          <h2>История обновлений</h2>
          <Link to="/diagnostics">Диагностика</Link>
        </div>
        <div ref={setHistoryTarget} />
        {!selected?.moy && !selected?.ozon && (
          <p>История появится после выбора аккаунта в настройках сервиса.</p>
        )}
      </section>
    </div>
  );
}
