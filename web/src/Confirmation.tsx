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
  const [resultContext, setResultContext] = useState<Pending | null>(null);
  const [failure, setFailure] = useState<{
    reviewId: string;
    code: string;
  } | null>(null);
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
      setResultContext(p);
      setPending(null);
      pendingRef.current = null;
      await Promise.all(
        [
          "products",
          "review-history",
          "review-targets",
          "warehouses",
          "warehouse-review",
          "diagnostics",
        ].map((key) => client.invalidateQueries({ queryKey: [key] })),
      );
    } catch (error) {
      const code = error instanceof ApiError ? error.code : undefined;
      if (error instanceof ApiError && error.status === 401) {
        setSessionLost(true);
        setMessage(
          "Сессия завершилась. Подтверждение могло сохраниться. Войдите снова и проверьте историю исходной пары аккаунтов; в новой сессии эта проверка не повторяется.",
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
        setFailure({ reviewId: p.intent.reviewId, code });
        setMessage(
          `${code}: Подтверждение отклонено. Обновите данные, устраните конфликт и проверьте соответствие заново.`,
        );
        setPending(null);
        pendingRef.current = null;
      } else if (code === "IDEMPOTENCY_CONFLICT") {
        p.uncertain = true;
        setMessage(
          "IDEMPOTENCY_CONFLICT: Проверьте историю соответствий. Не создавайте новое подтверждение, пока не уточните результат.",
        );
      } else {
        p.uncertain = true;
        setMessage(
          "Результат подтверждения неизвестен: запрос мог сохраниться. Повторите то же подтверждение, чтобы уточнить результат.",
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
    kind: "product" | "warehouse" = "product",
  ) {
    if (pendingRef.current || lock.current) return;
    const p = {
      scope: { ...scope },
      product,
      intent: createConfirmationIntent(reviewId, explanation, kind),
      uncertain: false,
    };
    pendingRef.current = p;
    setPending(p);
    setResult(null);
    setResultContext(null);
    setFailure(null);
    setSessionLost(false);
    void submit(p);
  }
  return {
    pending,
    busy,
    message,
    sessionLost,
    result,
    resultContext,
    failure,
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
  const visibleMessage = message.replace(/^[A-Z_]+: /, "");
  const errorCode = message.match(/^([A-Z_]+): /)?.[1];
  return (
    <div className="notice confirmation-notice" aria-live="polite">
      {pending && (
        <p>
          {busy
            ? "Сохраняем подтверждение соответствия. Смена аккаунтов не отменяет запрос."
            : "Результат относится к исходной паре аккаунтов."}
        </p>
      )}
      {visibleMessage && <p role="alert">{visibleMessage}</p>}
      {pending &&
        !sessionLost &&
        !busy &&
        (same ? (
          <button onClick={confirmation.retry}>
            Повторить то же подтверждение
          </button>
        ) : (
          <button onClick={() => restore(pending.scope)}>
            К исходным аккаунтам
          </button>
        ))}
      {pending && sessionLost && (
        <button onClick={confirmation.dismissAfterSessionLoss}>
          Проверю историю соответствий
        </button>
      )}
      {result && (
        <p role="status">
          Соответствие подтверждено ·{" "}
          {new Date(result.confirmed_at).toLocaleString("ru-RU")}
          {result.idempotent_replay
            ? ". Ранее сохранённое подтверждение восстановлено."
            : "."}
        </p>
      )}
      {(pending || result || errorCode) && (
        <details className="technical-details">
          <summary>Технические детали</summary>
          {errorCode && (
            <p>
              Код результата: <code>{errorCode}</code>
            </p>
          )}
          {pending && (
            <p>
              Объект: <code>{pending.product}</code>
              <br />
              МойСклад: <code>{pending.scope.moy}</code>
              <br />
              Ozon: <code>{pending.scope.ozon}</code>
            </p>
          )}
          {result && (
            <p>
              Соответствие: <code>{result.mapping_id}</code> · версия{" "}
              {result.mapping_version}
              <br />
              Основание: <code>{result.evidence_class}</code>
            </p>
          )}
        </details>
      )}
    </div>
  );
}
