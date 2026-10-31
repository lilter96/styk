import { useCallback, useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  api,
  params,
  post,
  type Account,
  type Batch,
  type ImportOperation,
  type ImportProvider,
  type ImportScope,
} from "./api";
import { ImportPanel } from "./ImportPanel";
import { IntegrationMetrics } from "./IntegrationCardParts";
import type { ImportSubmissionState } from "./ImportSubmission";

export type ConnectionView = {
  provider: ImportProvider;
  account?: string;
  status: string;
  credentials_stored?: boolean;
  check?: { checked_at: string; reason?: string };
  import?: Batch;
};
export function IntegrationAccountCard({
  tenant,
  provider,
  account,
  imports,
  onSettings,
  historyTarget,
  onStateChange,
}: {
  tenant: string;
  provider: ImportProvider;
  account: Account;
  imports: ImportSubmissionState;
  onSettings: () => void;
  historyTarget?: HTMLElement | null;
  onStateChange?: (
    scope: ImportScope,
    state: ImportOperation["state"] | undefined,
  ) => void;
}) {
  const root = useRef<HTMLElement>(null);
  const [visible, setVisible] = useState(false),
    [operationState, setOperationState] = useState<ImportOperation["state"]>(),
    [probeBusy, setProbeBusy] = useState(false),
    [probeError, setProbeError] = useState("");
  const probeLock = useRef(false);
  useEffect(() => {
    if (!root.current) return;
    if (typeof IntersectionObserver === "undefined") {
      setVisible(true);
      return;
    }
    const observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((entry) => entry.isIntersecting)) {
          setVisible(true);
          observer.disconnect();
        }
      },
      { rootMargin: "100px" },
    );
    observer.observe(root.current);
    return () => observer.disconnect();
  }, []);
  const scope = { tenant, provider, account: account.account };
  const query = useQuery({
    queryKey: ["integrations", tenant, provider, account.account],
    queryFn: ({ signal }) =>
      api<{ providers: ConnectionView[] }>(
        `/api/integrations?${params({ tenant, [provider + "_account"]: account.account })}`,
        signal,
      ),
    enabled: visible,
    retry: false,
  });
  const connection = query.data?.providers.find(
    (entry) => entry.provider === provider && entry.account === account.account,
  );
  const configured = !!connection && connection.status !== "NOT_CONFIGURED";
  const problem =
    connection?.status === "VERIFICATION_FAILED"
      ? connection.check?.reason === "INVALID_CREDENTIALS"
        ? "Ключ доступа не принят. Обновите его в настройках."
        : "Не удалось проверить доступ. Проверьте подключение."
      : undefined;
  const active = operationState === "QUEUED" || operationState === "RUNNING";
  const status = query.isError
    ? "Состояние недоступно"
    : operationState === "OUTCOME_UNKNOWN"
      ? "Результат уточняется"
      : operationState === "QUEUED"
        ? "В очереди"
        : operationState === "RUNNING"
          ? "Загружаем…"
          : operationState === "FAILED"
            ? "Ошибка загрузки"
            : !connection
              ? "Загружаем состояние…"
              : problem
                ? "Требует проверки"
                : connection.status === "VERIFIED"
                  ? "Работает"
                  : configured
                    ? "Доступ не проверен"
                    : connection.credentials_stored
                      ? "Отключён"
                      : "Не подключён";
  const tone =
    query.isError || operationState === "FAILED"
      ? "error"
      : problem || operationState === "OUTCOME_UNKNOWN"
        ? "warning"
        : active
          ? "active"
          : connection?.status === "VERIFIED"
            ? "verified"
            : "";
  const stateChanged = useCallback(
    (current: ImportScope, state: ImportOperation["state"] | undefined) => {
      setOperationState(state);
      onStateChange?.(current, state);
    },
    [onStateChange],
  );
  async function probe() {
    if (probeLock.current) return;
    probeLock.current = true;
    setProbeBusy(true);
    setProbeError("");
    try {
      await post(`/api/integrations/${provider}/probe`, {
        tenant,
        account: account.account,
      });
      await query.refetch();
    } catch {
      setProbeError("Не удалось проверить подключение. Повторите позже.");
    } finally {
      probeLock.current = false;
      setProbeBusy(false);
    }
  }
  return (
    <article
      ref={root}
      className="integration-account-card"
      aria-label={account.name || "Аккаунт " + provider}
      data-account={account.account}
      data-provider={provider}
    >
      <header className="account-card-header">
        <h3 title={account.name || "Аккаунт"}>{account.name || "Аккаунт"}</h3>
        <span className={`connection-state ${tone}`}>{status}</span>
      </header>
      {query.isError ? (
        <>
          <IntegrationMetrics provider={provider} />
          <p className="integration-empty-warning" role="alert">
            Не удалось загрузить данные аккаунта.{" "}
            <button
              className="text-action"
              onClick={() => void query.refetch()}
            >
              Повторить
            </button>
          </p>
          <button className="text-action" onClick={onSettings}>
            Настройки
          </button>
        </>
      ) : connection ? (
        <ImportPanel
          scope={scope}
          configured={configured}
          submission={imports}
          latestBatch={connection.import}
          accountLabel={account.name}
          compact
          historyManaged
          historyTarget={historyTarget}
          onStateChange={stateChanged}
          onSettings={onSettings}
          onProbe={() => void probe()}
          probeBusy={probeBusy}
          connectionProblem={problem}
          repairCredentials={connection.check?.reason === "INVALID_CREDENTIALS"}
        />
      ) : (
        <p className="account-loading" role="status">
          Загружаем данные аккаунта…
        </p>
      )}
      {probeError && (
        <p role="alert" className="integration-empty-warning">
          {probeError}
        </p>
      )}
    </article>
  );
}
