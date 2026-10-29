import { useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import {
  ApiError,
  createConfirmationIntent,
  sendConfirmation,
  type ConfirmationIntent,
  type ConfirmationResult,
} from "./api";

export type Scope = { tenant: string; moy: string; ozon: string };
type Pending = {
  intent: ConfirmationIntent;
  scope: Scope;
  product: string;
  uncertain: boolean;
};
export function useConfirmation() {
  const client = useQueryClient();
  const [pending, setPending] = useState<Pending | null>(null);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const [sessionLost, setSessionLost] = useState(false);
  const [result, setResult] = useState<ConfirmationResult | null>(null);
  const lock = useRef(false);
  const pendingRef = useRef<Pending | null>(null);
  async function submit(p: Pending) {
    if (lock.current) return;
    lock.current = true;
    setBusy(true);
    setMessage("");
    try {
      const value = await sendConfirmation(
        p.intent,
        AbortSignal.timeout(30000),
      );
      setResult(value);
      setPending(null);
      pendingRef.current = null;
      await Promise.all(
        ["products", "review-history", "diagnostics"].map((key) =>
          client.invalidateQueries({ queryKey: [key] }),
        ),
      );
    } catch (error) {
      const code = error instanceof ApiError ? error.code : undefined;
      if (error instanceof ApiError && error.status === 401) {
        setSessionLost(true);
        setMessage(
          "The session ended. The request may have committed. After signing in, inspect mapping history for the original account pair; the new session cannot replay this review.",
        );
        void client.invalidateQueries({ queryKey: ["session"] });
      } else if (
        !p.uncertain &&
        code &&
        [
          "REVIEW_EXPIRED",
          "REVIEW_STALE",
          "MAPPING_CONFLICT",
          "INVALID_REQUEST",
          "FORBIDDEN",
          "REVIEW_NOT_FOUND",
        ].includes(code)
      ) {
        setMessage(
          `${code}: confirmation was rejected. Create a new review or refresh the product and resolve the conflict.`,
        );
        setPending(null);
        pendingRef.current = null;
      } else if (code === "IDEMPOTENCY_CONFLICT") {
        p.uncertain = true;
        setMessage(
          "IDEMPOTENCY_CONFLICT: inspect mapping history; do not create another confirmation until its outcome is known.",
        );
      } else {
        p.uncertain = true;
        setMessage(
          "Outcome unknown: the request may have committed. Retry the exact same request to reconcile it.",
        );
      }
    } finally {
      lock.current = false;
      setBusy(false);
    }
  }
  function start(
    scope: Scope,
    product: string,
    reviewId: string,
    explanation: string,
  ) {
    if (pendingRef.current || lock.current) return;
    const p = {
      scope: { ...scope },
      product,
      intent: createConfirmationIntent(reviewId, explanation),
      uncertain: false,
    };
    pendingRef.current = p;
    setPending(p);
    setResult(null);
    setSessionLost(false);
    void submit(p);
  }
  return {
    pending,
    busy,
    message,
    sessionLost,
    result,
    start,
    retry: () => {
      if (pendingRef.current && !sessionLost) void submit(pendingRef.current);
    },
    dismissAfterSessionLoss: () => {
      if (!lock.current && sessionLost) {
        pendingRef.current = null;
        setPending(null);
        setSessionLost(false);
        setMessage("");
      }
    },
  };
}
export type Confirmation = ReturnType<typeof useConfirmation>;
export function ConfirmationNotice({
  confirmation,
  scope,
  restore,
}: {
  confirmation: Confirmation;
  scope: Scope;
  restore: (scope: Scope) => void;
}) {
  const { pending, busy, message, sessionLost, result } = confirmation;
  const same =
    pending &&
    pending.scope.tenant === scope.tenant &&
    pending.scope.moy === scope.moy &&
    pending.scope.ozon === scope.ozon;
  if (!pending && !message && !result) return null;
  return (
    <div className="notice" aria-live="polite">
      {pending && (
        <p>
          Confirmation for product {pending.product} in {pending.scope.tenant} /{" "}
          {pending.scope.moy} / {pending.scope.ozon}.{" "}
          {busy
            ? "Request in progress; switching accounts does not cancel the transaction."
            : "The original request remains in memory."}
        </p>
      )}
      {message && <p role="alert">{message}</p>}
      {pending &&
        !sessionLost &&
        !busy &&
        (same ? (
          <button onClick={confirmation.retry}>Retry same confirmation</button>
        ) : (
          <button onClick={() => restore(pending.scope)}>
            Return to original account scope
          </button>
        ))}
      {pending && sessionLost && (
        <button onClick={confirmation.dismissAfterSessionLoss}>
          I will inspect mapping history
        </button>
      )}
      {result && (
        <p>
          Confirmed mapping {result.mapping_id}, version{" "}
          {result.mapping_version}; {result.evidence_class};{" "}
          {new Date(result.confirmed_at).toLocaleString()}.{" "}
          {result.idempotent_replay &&
            "Existing confirmation recovered by idempotent replay."}{" "}
          Principal: LOCAL_OPERATOR_SESSION.
        </p>
      )}
    </div>
  );
}
