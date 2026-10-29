import { useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import {
  ApiError,
  importProviders,
  sameImportScope,
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
        value.operation.provider !== p.intent.scope.provider ||
        value.operation.operation_type !==
          importProviders[p.intent.scope.provider].operationType
      )
        throw new Error("Unexpected scoped response");
      setResult(value);
      ref.current = null;
      setPending(null);
      setSessionLost(false);
      setMessage(
        value.idempotent_replay
          ? "Ранее созданная загрузка найдена. Повторный запрос не создал новую."
          : "Запрос на загрузку принят.",
      );
      void client.invalidateQueries({
        queryKey: [
          "import-history",
          p.intent.scope.tenant,
          p.intent.scope.provider,
          p.intent.scope.account,
        ],
      });
      void client.invalidateQueries({
        queryKey: [
          "import-detail",
          p.intent.scope.tenant,
          p.intent.scope.provider,
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
          "Сессия завершилась. Загрузка могла попасть в очередь. Войдите снова и проверьте историю исходного аккаунта в том же рабочем пространстве перед новым запросом.",
        );
        void client.invalidateQueries({ queryKey: ["session"] });
      } else if (code === "IDEMPOTENCY_CONFLICT") {
        p.uncertain = true;
        setPending({ ...p });
        setHistoryInspected(false);
        setMessage(
          "IDEMPOTENCY_CONFLICT: Не удалось однозначно сопоставить запрос. Проверьте историю исходного аккаунта перед новой загрузкой.",
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
          `${code}: Запрос отклонён. Проверьте выбранный аккаунт и историю загрузок.`,
        );
        void client.invalidateQueries({
          queryKey: [
            "import-history",
            p.intent.scope.tenant,
            p.intent.scope.provider,
            p.intent.scope.account,
          ],
        });
      } else {
        p.uncertain = true;
        setPending({ ...p });
        setMessage(
          "Результат отправки неизвестен: загрузка уже могла попасть в очередь или начаться. Повторите тот же запрос, чтобы уточнить результат без создания новой загрузки.",
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
        "Сессия завершилась. Загрузка могла попасть в очередь. Войдите снова и проверьте историю исходного аккаунта перед новым запросом.",
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
      if (ref.current && sameImportScope(ref.current.intent.scope, scope))
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
    (result.operation.provider !== scope.provider ||
      result.operation.tenant !== scope.tenant ||
      result.operation.account !== scope.account)
  )
    return null;
  const same = pending && sameImportScope(pending.intent.scope, scope);
  const original = pending?.intent.scope;
  const name =
    (original?.provider || result?.operation.provider) === "moysklad"
      ? "МойСклад"
      : "Ozon";
  const codedMessage = message.match(/^([A-Z_]+): (.*)$/s);
  const visibleMessage = codedMessage?.[2] || message;
  return (
    <div
      className={`notice import-submission-notice ${!pending && !codedMessage ? "is-success" : ""}`}
      aria-live="polite"
    >
      {pending && (
        <p>
          <strong>
            {busy
              ? `Отправляем запрос ${name}`
              : `Проверяем результат запроса ${name}`}
          </strong>
        </p>
      )}
      {pending && busy && (
        <p>Смена аккаунта не отменяет отправленный запрос.</p>
      )}
      {visibleMessage && (
        <p role={pending || codedMessage ? "alert" : "status"}>
          {visibleMessage}
        </p>
      )}
      {pending && !busy && !same && (
        <>
          <p>
            Запрос относится к другому аккаунту. Вернитесь к нему для проверки
            результата.
          </p>
          <button onClick={() => restore(pending.intent.scope)}>
            Вернуться к исходному аккаунту
          </button>
        </>
      )}
      {pending &&
        !busy &&
        same &&
        !sessionLost &&
        !message.startsWith("IDEMPOTENCY_CONFLICT") && (
          <button onClick={submission.retry}>Повторить тот же запрос</button>
        )}
      {pending &&
        !busy &&
        same &&
        (sessionLost || message.startsWith("IDEMPOTENCY_CONFLICT")) && (
          <p>
            Перед продолжением проверьте историю загрузок исходного аккаунта.
          </p>
        )}
      {pending &&
        !busy &&
        same &&
        historyInspected &&
        (sessionLost || message.startsWith("IDEMPOTENCY_CONFLICT")) && (
          <button onClick={submission.dismiss}>История проверена</button>
        )}
      {(original || codedMessage) && (
        <details className="technical-details">
          <summary>Технические детали</summary>
          {original && (
            <p>
              Рабочее пространство: <code>{original.tenant}</code>
              <br />
              Аккаунт: <code>{original.account}</code>
            </p>
          )}
          {codedMessage && (
            <p>
              Код ошибки: <code>{codedMessage[1]}</code>
            </p>
          )}
        </details>
      )}
    </div>
  );
}
