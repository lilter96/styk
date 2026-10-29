import { useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { ApiError, api, authFetch, params, type ImportProvider } from "./api";

export type CredentialScope = {
  tenant: string;
  provider: ImportProvider;
  account: string;
};
export type CredentialMetadata = CredentialScope & {
  name: string;
  revision: number;
  enabled: boolean;
  updated_at: string;
  checked_at: string;
  last_request_id: string;
};
export type CredentialStatus = {
  storage_available: boolean;
  credential: CredentialMetadata | null;
};
type Intent = {
  scope: Readonly<CredentialScope>;
  path: string;
  body: string;
  requestID: string;
  uncertain: boolean;
};
const labels = { moysklad: "МойСклад", ozon: "Ozon" };
const rejected: Record<string, string> = {
  INVALID_REQUEST: "Проверьте название подключения и данные доступа.",
  CREDENTIAL_STORAGE_UNAVAILABLE:
    "Сохранение ключей пока не настроено на сервере. Обратитесь к администратору.",
  CREDENTIAL_VERSION_CONFLICT:
    "Настройки уже изменились. Обновите их перед сохранением.",
  OWNERSHIP_CONFLICT:
    "Аккаунт подключён к другому рабочему пространству. Перенос недоступен.",
  INVALID_CREDENTIALS:
    "Сервис не принял ключ. Проверьте его или создайте новый.",
  PERMISSION_DENIED:
    "У ключа недостаточно прав. Проверьте разрешения в сервисе.",
  ACCOUNT_IDENTITY_MISMATCH:
    "Ключ относится к другому аккаунту. Выберите правильное подключение.",
  PROBE_FAILED:
    "Не удалось проверить доступ. Ключ не сохранён. Попробуйте ещё раз.",
  TIMEOUT: "Сервис не ответил вовремя. Ключ не сохранён. Попробуйте ещё раз.",
  PROBE_TIMEOUT:
    "Сервис не ответил вовремя. Ключ не сохранён. Попробуйте ещё раз.",
  CANCELLED: "Проверка прервана. Ключ не сохранён.",
  CREDENTIAL_NOT_FOUND: "Подключение не найдено. Обновите настройки.",
  FORBIDDEN: "Запрос отклонён. Обновите страницу и войдите снова.",
};
export function sameCredentialScope(a: CredentialScope, b: CredentialScope) {
  return (
    a.tenant === b.tenant &&
    a.provider === b.provider &&
    a.account === b.account
  );
}
export function useCredentialSubmission() {
  const client = useQueryClient();
  const [pending, setPending] = useState<Intent | null>(null);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const [code, setCode] = useState("");
  const [sessionLost, setSessionLost] = useState(false);
  const [inspected, setInspected] = useState(false);
  const [feedbackScope, setFeedbackScope] = useState<CredentialScope | null>(
    null,
  );
  const [result, setResult] = useState<CredentialMetadata | null>(null);
  const current = useRef<Intent | null>(null),
    lock = useRef(false);
  async function finish(meta: CredentialMetadata, replayed = false) {
    current.current = null;
    setPending(null);
    setResult(meta);
    setSessionLost(false);
    setInspected(false);
    setCode("");
    setMessage(
      replayed
        ? "Результат прежнего запроса найден. Текущее состояние смотрите в карточке подключения."
        : meta.enabled
          ? "Ключ сохранён, подключение проверено."
          : "Подключение отключено. Загруженные данные и история сохранены.",
    );
    for (const key of [
      "integrations",
      "credentials",
      "accounts",
      "diagnostics",
    ])
      await client.invalidateQueries({ queryKey: [key] });
  }
  function validate(meta: CredentialMetadata, intent: Intent) {
    const input = JSON.parse(intent.body) as { expected_revision: number };
    return (
      !!meta &&
      meta.revision === input.expected_revision + 1 &&
      meta.enabled === !intent.path.endsWith("/disconnect") &&
      meta.tenant === intent.scope.tenant &&
      meta.provider === intent.scope.provider &&
      (meta.account === intent.scope.account ||
        (intent.scope.provider === "moysklad" &&
          intent.scope.account === "" &&
          !!meta.account)) &&
      meta.last_request_id === intent.requestID
    );
  }
  async function send(intent: Intent) {
    if (lock.current) return;
    lock.current = true;
    setBusy(true);
    setMessage("");
    setCode("");
    try {
      const response = await authFetch(intent.path, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: intent.body,
        signal: AbortSignal.timeout(30000),
      });
      if (!response.ok) {
        const data = await response.json().catch(() => ({}));
        throw new ApiError(
          response.status,
          data.error || "CREDENTIAL_SAVE_FAILED",
        );
      }
      const data = (await response.json()) as {
        credential: CredentialMetadata;
        idempotent_replay?: boolean;
      };
      if (!validate(data.credential, intent))
        throw new Error("Scoped credential response unavailable");
      await finish(data.credential, !!data.idempotent_replay);
    } catch (error) {
      const value = error instanceof ApiError ? error.code || "" : "";
      setCode(value);
      if (!intent.uncertain && value && rejected[value]) {
        current.current = null;
        setPending(null);
        setMessage(rejected[value]);
      } else {
        intent.uncertain = true;
        setPending({ ...intent });
        setInspected(false);
        if (error instanceof ApiError && error.status === 401) {
          setSessionLost(true);
          void client.invalidateQueries({ queryKey: ["session"] });
        }
        setMessage(
          value === "IDEMPOTENCY_CONFLICT"
            ? "Этот запрос уже использован с другими настройками. Проверьте текущее подключение перед новым сохранением."
            : "Результат сохранения неизвестен. Ключ мог быть сохранён. Повторите тот же запрос или проверьте состояние подключения.",
        );
      }
    } finally {
      lock.current = false;
      setBusy(false);
    }
  }
  function start(
    scope: CredentialScope,
    values: { name: string; secret: string; expectedRevision: number },
  ) {
    if (current.current || lock.current) return;
    const requestID = crypto.randomUUID();
    const frozen = Object.freeze({ ...scope });
    const intent: Intent = {
      scope: frozen,
      path: `/api/integrations/${scope.provider}/credentials`,
      requestID,
      body: JSON.stringify({
        tenant: scope.tenant,
        account: scope.account,
        name: values.name.trim(),
        secret: values.secret,
        expected_revision: values.expectedRevision,
        request_id: requestID,
        acknowledge: true,
      }),
      uncertain: false,
    };
    setFeedbackScope(scope);
    current.current = intent;
    setPending(intent);
    setResult(null);
    setInspected(false);
    void send(intent);
  }
  function disconnect(scope: CredentialScope, expectedRevision: number) {
    if (current.current || lock.current) return;
    const requestID = crypto.randomUUID(),
      frozen = Object.freeze({ ...scope });
    const intent: Intent = {
      scope: frozen,
      path: `/api/integrations/${scope.provider}/disconnect`,
      requestID,
      body: JSON.stringify({
        tenant: scope.tenant,
        account: scope.account,
        expected_revision: expectedRevision,
        request_id: requestID,
        acknowledge: true,
      }),
      uncertain: false,
    };
    setFeedbackScope(scope);
    current.current = intent;
    setPending(intent);
    setResult(null);
    void send(intent);
  }
  async function inspect() {
    const intent = current.current;
    if (!intent || lock.current) return;
    lock.current = true;
    setBusy(true);
    try {
      const data = await api<CredentialStatus>(
        `/api/integrations/${intent.scope.provider}/credentials?${params({ tenant: intent.scope.tenant, account: intent.scope.account })}`,
      );
      if (
        data.credential &&
        (data.credential.tenant !== intent.scope.tenant ||
          data.credential.provider !== intent.scope.provider ||
          (intent.scope.account !== "" &&
            data.credential.account !== intent.scope.account))
      )
        throw new Error("Scoped metadata unavailable");
      setInspected(true);
      setSessionLost(false);
      if (
        code !== "IDEMPOTENCY_CONFLICT" &&
        data.credential &&
        validate(data.credential, intent)
      )
        await finish(data.credential);
      else
        setMessage(
          "Состояние подключения проверено. Сохранение этого запроса не подтверждено. Можно повторить тот же запрос или закрыть его после проверки настроек.",
        );
    } catch {
      setMessage(
        "Не удалось проверить состояние. Не сохраняйте новый ключ, пока результат предыдущего запроса неизвестен.",
      );
    } finally {
      lock.current = false;
      setBusy(false);
    }
  }
  return {
    pending,
    busy,
    message,
    code,
    sessionLost,
    inspected,
    result,
    feedbackScope,
    start,
    disconnect,
    inspect,
    retry: () => {
      if (current.current && !sessionLost && code !== "IDEMPOTENCY_CONFLICT")
        void send(current.current);
    },
    dismiss: () => {
      if (!lock.current && inspected) {
        current.current = null;
        setPending(null);
        setMessage("");
        setCode("");
      }
    },
  };
}
export type CredentialSubmissionState = ReturnType<
  typeof useCredentialSubmission
>;
export function CredentialNotice({
  submission,
  tenant,
  restore,
}: {
  submission: CredentialSubmissionState;
  tenant: string;
  restore: (scope: CredentialScope) => void;
}) {
  const { pending, busy, message, result } = submission;
  if (!pending && (!result || result.tenant !== tenant || !message))
    return null;
  return (
    <div className="integration-feedback" role="alert">
      {pending && (
        <strong>
          {busy
            ? "Проверяем и сохраняем ключ"
            : "Результат сохранения требует проверки"}{" "}
          · {labels[pending.scope.provider]}
        </strong>
      )}
      {message && <p>{message}</p>}
      {pending && !busy && (
        <div className="provider-actions">
          <button className="secondary" onClick={() => restore(pending.scope)}>
            К исходному подключению
          </button>
          {!submission.sessionLost &&
            submission.code !== "IDEMPOTENCY_CONFLICT" && (
              <button onClick={submission.retry}>
                Повторить тот же запрос
              </button>
            )}
          <button
            className="text-action"
            onClick={() => void submission.inspect()}
          >
            Проверить сохранение
          </button>
          {submission.inspected && (
            <button className="text-action" onClick={submission.dismiss}>
              Статус проверен, закрыть запрос
            </button>
          )}
        </div>
      )}
    </div>
  );
}
