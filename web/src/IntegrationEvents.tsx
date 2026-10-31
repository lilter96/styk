import { useQueries } from "@tanstack/react-query";
import {
  api,
  importAccount,
  params,
  type ImportOperation,
  type ImportProvider,
  type Account,
} from "./api";

type Probe = {
  provider: ImportProvider;
  check?: { checked_at: string; reason?: string };
  status: string;
};
const names = { moysklad: "МойСклад", ozon: "Ozon" };
const results: Record<ImportOperation["state"], string> = {
  QUEUED: "Загрузка в очереди",
  RUNNING: "Загружаем данные",
  SUCCEEDED: "Каталог загружен",
  FAILED: "Не удалось загрузить данные",
  CANCELLED: "Загрузка отменена",
  OUTCOME_UNKNOWN: "Проверяем результат загрузки",
};
export function IntegrationEvents({
  tenant,
  accounts,
  accountList,
  providers,
  onSupport,
  onRepair,
  onHistory,
}: {
  tenant: string;
  accounts: Partial<Record<ImportProvider, string>>;
  accountList?: Account[];
  providers: Probe[];
  onSupport: (operation?: ImportOperation) => void;
  onRepair: (provider: ImportProvider) => void;
  onHistory: () => void;
}) {
  const scopes = accountList
    ? accountList
        .filter(
          (account) =>
            account.source === "moysklad" || account.source === "ozon",
        )
        .map((account) => ({
          tenant,
          provider: account.source as ImportProvider,
          account: account.account,
        }))
    : (["moysklad", "ozon"] as const).map((provider) => ({
        tenant,
        provider,
        account: accounts[provider] || "",
      }));
  const queries = useQueries({
    queries: scopes.map((scope) => ({
      queryKey: ["import-history", tenant, scope.provider, scope.account, 0],
      queryFn: ({ signal }: { signal: AbortSignal }) =>
        api<{ items: ImportOperation[]; has_next: boolean; offset: number }>(
          `/api/imports?${params({ tenant, ...importAccount(scope), limit: 10, offset: 0 })}`,
          signal,
        ),
      enabled: !!tenant && !!scope.account,
      retry: false,
      refetchOnWindowFocus: false,
    })),
  });
  const operations = queries.flatMap((q) => q.data?.items || []);
  const events: {
    id: string;
    at: string;
    provider: ImportProvider;
    text: string;
    tone: string;
    operation?: ImportOperation;
    repair?: boolean;
    status: string;
    accountName?: string;
  }[] = operations.map((op) => ({
    id: op.id,
    at: op.finished_at || op.started_at || op.created_at,
    provider: op.provider as ImportProvider,
    text:
      op.state === "SUCCEEDED"
        ? `${results[op.state]} · обнаружено товаров: ${op.counts.products}, складов: ${op.counts.warehouses}`
        : results[op.state],
    tone:
      op.state === "FAILED" || op.state === "OUTCOME_UNKNOWN"
        ? "warning"
        : op.state === "CANCELLED"
          ? "neutral"
          : op.state === "RUNNING" || op.state === "QUEUED"
            ? "active"
            : "",
    operation: op,
    accountName: accountList?.find(
      (account) =>
        account.source === op.provider && account.account === op.account,
    )?.name,
    status:
      op.state === "SUCCEEDED"
        ? "Успешно"
        : op.state === "FAILED"
          ? "Ошибка"
          : op.state === "RUNNING"
            ? "В процессе"
            : op.state === "OUTCOME_UNKNOWN"
              ? "Уточняется"
              : op.state === "CANCELLED"
                ? "Отменено"
                : "Ожидает",
  }));
  for (const p of providers)
    if (p.check)
      events.push({
        id: `probe:${p.provider}:${p.check.checked_at}`,
        at: p.check.checked_at,
        provider: p.provider,
        text:
          p.status === "VERIFIED"
            ? "Доступ проверен"
            : p.check.reason === "INVALID_CREDENTIALS"
              ? "Ключ не принят"
              : "Не удалось проверить доступ",
        tone: p.status === "VERIFIED" ? "" : "warning",
        repair: p.status !== "VERIFIED",
        status: p.status === "VERIFIED" ? "Успешно" : "Ошибка",
      });
  events.sort((a, b) => Date.parse(b.at) - Date.parse(a.at));
  const shown = events.slice(0, 3);
  const latest = [...operations].sort(
    (a, b) => Date.parse(b.created_at) - Date.parse(a.created_at),
  )[0];
  return (
    <section className="updates-history" aria-label="Последние события">
      <div className="history-heading">
        <h2>Последние события</h2>
        <button className="text-action" onClick={() => onSupport(latest)}>
          Диагностика для поддержки
        </button>
      </div>
      <div className="integration-events-scroll">
        <table className="integration-events-table">
          <thead>
            <tr>
              <th>Время</th>
              <th>Сервис</th>
              <th>Событие</th>
              <th>Статус</th>
              <th>Действия</th>
            </tr>
          </thead>
          <tbody>
            {shown.map((event) => (
              <tr key={event.id}>
                <td>
                  <time
                    dateTime={event.at}
                    title={new Date(event.at).toLocaleString("ru-RU")}
                  >
                    {new Date(event.at).toLocaleString("ru-RU", {
                      ...(new Date(event.at).toDateString() !==
                      new Date().toDateString()
                        ? { day: "numeric" as const, month: "short" as const }
                        : {}),
                      hour: "2-digit",
                      minute: "2-digit",
                    })}
                  </time>
                </td>
                <td>
                  <span className={`event-provider ${event.tone}`}>
                    {names[event.provider]}
                  </span>
                </td>
                <td>
                  {event.accountName && (
                    <small className="event-account-name">
                      {event.accountName}
                    </small>
                  )}
                  {event.text}
                </td>
                <td>
                  <span className={`integration-event-status ${event.tone}`}>
                    {event.status}
                  </span>
                </td>
                <td>
                  <button
                    className="text-action"
                    onClick={() =>
                      event.repair
                        ? onRepair(event.provider)
                        : onSupport(event.operation)
                    }
                  >
                    {event.repair ? "Что делать" : "Подробнее"}
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {shown.length === 0 && (
        <p className="history-empty">
          {queries.some((q) => q.isError)
            ? "События пока недоступны. Перед новой загрузкой проверьте историю аккаунта."
            : queries.some((q) => q.isLoading)
              ? "Загружаем события…"
              : "Здесь появятся результаты проверок и загрузок."}
        </p>
      )}
      <div className="history-footer">
        <button className="text-action" onClick={onHistory}>
          История обновлений
        </button>
      </div>
    </section>
  );
}
