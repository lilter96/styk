import { historyResult } from "./OrdersHistoryPresentation";

export function HistoryStatusIcon({ tone }: { tone: string }) {
  return (
    <svg
      width="20"
      height="20"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.8"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      {tone === "warning" ? (
        <>
          <path d="m12 3 10 18H2L12 3Z" />
          <path d="M12 9v5m0 3h.01" />
        </>
      ) : (
        <>
          <circle cx="12" cy="12" r="9" />
          {tone === "success" ? (
            <path d="m7 12 3 3 7-7" />
          ) : tone === "danger" ? (
            <path d="m8 8 8 8m-8 0 8-8" />
          ) : tone === "progress" ? (
            <path d="M12 6v6l4 2" />
          ) : tone === "pending" ? null : (
            <path d="M12 7v6m0 4h.01" />
          )}
        </>
      )}
    </svg>
  );
}
export function OrdersHistoryStatus({ state }: { state?: string }) {
  const result = historyResult(state);
  return (
    <span className={`history-status ${result.tone}`}>
      <HistoryStatusIcon tone={result.tone} />
      {result.label}
    </span>
  );
}
