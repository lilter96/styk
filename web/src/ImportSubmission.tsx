import { useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import {
  ApiError,
  createImportIntent,
  sendImportIntent,
  type ImportIntent,
  type ImportScope,
  type ImportSubmission,
} from "./api";

type Pending = { intent: ImportIntent; uncertain: boolean };
export function useImportSubmission() {
  const client = useQueryClient();
  const [pending, setPending] = useState<Pending | null>(null);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const [sessionLost, setSessionLost] = useState(false);
  const [historyInspected, setHistoryInspected] = useState(false);
  const [result, setResult] = useState<ImportSubmission | null>(null);
  const ref = useRef<Pending | null>(null);
  const lock = useRef(false);
  async function submit(p: Pending) {
    if (lock.current) return;
    lock.current = true;
    setBusy(true);
    setMessage("");
    try {
      const value = await sendImportIntent(
        p.intent,
        AbortSignal.timeout(30000),
      );
      if (
        value.operation.tenant !== p.intent.scope.tenant ||
        value.operation.account !== p.intent.scope.account ||
        value.operation.operation_type !== "OZON_CATALOG_IMPORT"
      )
        throw new Error("Unexpected scoped response");
      setResult(value);
      ref.current = null;
      setPending(null);
      setSessionLost(false);
      setMessage(
        value.idempotent_replay
          ? "The existing import operation was recovered."
          : "Import request accepted.",
      );
      void client.invalidateQueries({
        queryKey: [
          "import-history",
          p.intent.scope.tenant,
          p.intent.scope.account,
        ],
      });
      void client.invalidateQueries({
        queryKey: [
          "import-detail",
          p.intent.scope.tenant,
          p.intent.scope.account,
        ],
      });
    } catch (error) {
      const code = error instanceof ApiError ? error.code : undefined;
      if (error instanceof ApiError && error.status === 401) {
        p.uncertain = true;
        setPending({ ...p });
        setSessionLost(true);
        setHistoryInspected(false);
        setMessage(
          "Your session ended. The import may have been queued. Sign in and inspect history for the original tenant and account before requesting another import.",
        );
        void client.invalidateQueries({ queryKey: ["session"] });
      } else if (code === "IDEMPOTENCY_CONFLICT") {
        p.uncertain = true;
        setPending({ ...p });
        setHistoryInspected(false);
        setMessage(
          "IDEMPOTENCY_CONFLICT: inspect the original account's import history before requesting another import.",
        );
      } else if (
        !p.uncertain &&
        code &&
        [
          "IMPORT_ALREADY_ACTIVE",
          "ACCOUNT_NOT_CONFIGURED",
          "IMPORT_NOT_FOUND",
          "INVALID_REQUEST",
          "INVALID_LIMITS",
          "FORBIDDEN",
        ].includes(code)
      ) {
        ref.current = null;
        setPending(null);
        setMessage(
          `${code}: the request was rejected. Review the account and import history.`,
        );
        void client.invalidateQueries({
          queryKey: [
            "import-history",
            p.intent.scope.tenant,
            p.intent.scope.account,
          ],
        });
      } else {
        p.uncertain = true;
        setPending({ ...p });
        setMessage(
          "Submission outcome unknown: the import may already be queued or running. Retry the exact same request to reconcile it.",
        );
      }
    } finally {
      lock.current = false;
      setBusy(false);
    }
  }
  function start(scope: ImportScope) {
    if (ref.current || lock.current) return;
    const p = { intent: createImportIntent(scope), uncertain: false };
    ref.current = p;
    setPending(p);
    setResult(null);
    setSessionLost(false);
    setHistoryInspected(false);
    void submit(p);
  }
  function markSessionLost() {
    if (ref.current) {
      ref.current.uncertain = true;
      setPending({ ...ref.current });
      setSessionLost(true);
      setHistoryInspected(false);
      setMessage(
        "The session ended. The import may have been queued. Sign in and inspect history for the original account before submitting another import.",
      );
    }
    void client.invalidateQueries({ queryKey: ["session"] });
  }
  return {
    pending,
    busy,
    message,
    sessionLost,
    historyInspected,
    result,
    start,
    markSessionLost,
    retry: () => {
      if (ref.current && !sessionLost) void submit(ref.current);
    },
    inspected: (scope: ImportScope) => {
      if (
        ref.current &&
        ref.current.intent.scope.tenant === scope.tenant &&
        ref.current.intent.scope.account === scope.account
      )
        setHistoryInspected(true);
    },
    dismiss: () => {
      if (!lock.current && historyInspected) {
        ref.current = null;
        setPending(null);
        setSessionLost(false);
        setMessage("");
        setHistoryInspected(false);
      }
    },
  };
}
export type ImportSubmissionState = ReturnType<typeof useImportSubmission>;
export function ImportSubmissionNotice({
  submission,
  scope,
  restore,
}: {
  submission: ImportSubmissionState;
  scope: ImportScope;
  restore: (scope: ImportScope) => void;
}) {
  const { pending, busy, message, sessionLost, historyInspected, result } =
    submission;
  if (!pending && !message) return null;
  if (
    !pending &&
    result &&
    (result.operation.tenant !== scope.tenant ||
      result.operation.account !== scope.account)
  )
    return null;
  const same =
    pending &&
    pending.intent.scope.tenant === scope.tenant &&
    pending.intent.scope.account === scope.account;
  return (
    <div className="notice" aria-live="polite">
      {pending && (
        <p>
          Import request for tenant {pending.intent.scope.tenant}, Ozon account{" "}
          {pending.intent.scope.account}.{" "}
          {busy
            ? "Request in progress; changing accounts does not cancel it."
            : "The original request is held in memory."}
        </p>
      )}
      {message && <p role="alert">{message}</p>}
      {result && !pending && (
        <p>
          Operation {result.operation.id} · tenant {result.operation.tenant} ·
          Ozon account {result.operation.account}.
        </p>
      )}
      {pending && !busy && !same && (
        <button onClick={() => restore(pending.intent.scope)}>
          Return to original import scope
        </button>
      )}
      {pending &&
        !busy &&
        same &&
        !sessionLost &&
        !message.startsWith("IDEMPOTENCY_CONFLICT") && (
          <button onClick={submission.retry}>Retry same import request</button>
        )}
      {pending &&
        !busy &&
        same &&
        (sessionLost || message.startsWith("IDEMPOTENCY_CONFLICT")) && (
          <p>Inspect scoped import history before continuing.</p>
        )}
      {pending &&
        !busy &&
        same &&
        historyInspected &&
        (sessionLost || message.startsWith("IDEMPOTENCY_CONFLICT")) && (
          <button onClick={submission.dismiss}>
            I inspected import history
          </button>
        )}
    </div>
  );
}
