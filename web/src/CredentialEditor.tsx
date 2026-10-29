import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api, params, type Account } from "./api";
import {
  sameCredentialScope,
  type CredentialScope,
  type CredentialStatus,
  type CredentialSubmissionState,
} from "./CredentialSubmission";

export function CredentialEditor({
  scope,
  name,
  registration,
  reason,
  accounts,
  onSelect,
  submission,
  onClose,
}: {
  scope: CredentialScope;
  name?: string;
  registration: string;
  reason?: string;
  accounts: Account[];
  onSelect?: (account: string) => void;
  submission: CredentialSubmissionState;
  onClose: () => void;
}) {
  const label = scope.provider === "moysklad" ? "МойСклад" : "Ozon";
  const secret = useRef<HTMLInputElement>(null);
  const [connectionName, setConnectionName] = useState(name || label);
  const [account, setAccount] = useState(scope.account);
  const [hasSecret, setHasSecret] = useState(false);
  const [ack, setAck] = useState(false);
  const [disconnecting, setDisconnecting] = useState(false);
  const [disconnectAck, setDisconnectAck] = useState(false);
  const [localError, setLocalError] = useState("");
  const [copied, setCopied] = useState(false);
  const query = useQuery({
    queryKey: ["credentials", scope.tenant, scope.provider, scope.account],
    queryFn: ({ signal }) =>
      api<CredentialStatus>(
        `/api/integrations/${scope.provider}/credentials?${params({ tenant: scope.tenant, account: scope.account })}`,
        signal,
      ),
    retry: false,
  });
  const metadata = scope.account ? query.data?.credential : null;
  const samePending =
    !!submission.pending &&
    sameCredentialScope(submission.pending.scope, { ...scope, account });
  useEffect(() => {
    if (metadata?.name) setConnectionName(metadata.name);
  }, [metadata?.name]);
  const finished =
    submission.result &&
    submission.result.tenant === scope.tenant &&
    submission.result.provider === scope.provider &&
    (submission.result.account === scope.account || !scope.account)
      ? submission.result
      : null;
  useEffect(() => {
    if (finished) {
      if (secret.current) secret.current.value = "";
      setHasSecret(false);
      setAck(false);
      setDisconnectAck(false);
      setDisconnecting(false);
    }
  }, [finished?.last_request_id]);
  useEffect(() => {
    if (
      finished?.enabled &&
      onSelect &&
      accounts.some((a) => a.account === finished.account)
    )
      onSelect(finished.account);
  }, [finished?.last_request_id, accounts]);
  function save() {
    if (!secret.current?.value || !ack || submission.pending) return;
    setLocalError("");
    try {
      submission.start(
        { ...scope, account },
        {
          name: connectionName,
          secret: secret.current.value,
          expectedRevision: metadata?.revision || 0,
        },
      );
      secret.current.value = "";
      setHasSecret(false);
    } catch {
      setLocalError("Не удалось создать защищённый запрос. Ключ не отправлен.");
    }
  }
  return (
    <div className="provider-settings-body">
      {registration === "VERIFICATION_FAILED" && (
        <div className="integration-feedback">
          {reason === "INVALID_CREDENTIALS" ? (
            <>
              <strong>Ключ не принят.</strong> Он мог истечь или быть отозван в{" "}
              {label}. Укажите новый ключ и проверьте подключение.
            </>
          ) : (
            <>
              <strong>Не удалось проверить доступ.</strong> Проверьте разрешения
              подключения и повторите проверку.
            </>
          )}
        </div>
      )}
      {accounts.length > 0 && (
        <label className="integration-field">
          Аккаунт для работы
          <select
            aria-label={`${label}: аккаунт`}
            value={scope.account}
            onChange={(e) => onSelect?.(e.target.value)}
          >
            <option value="">Новое подключение</option>
            {accounts.map((a, index) => (
              <option key={a.account} value={a.account}>
                {a.name || `Аккаунт ${index + 1}`}
              </option>
            ))}
          </select>
        </label>
      )}
      {query.isLoading && <p role="status">Загружаем настройки подключения…</p>}
      {query.error && (
        <div className="integration-feedback" role="alert">
          Не удалось проверить текущую версию настроек. Перед сохранением
          обновите её.{" "}
          <button className="text-action" onClick={() => void query.refetch()}>
            Повторить
          </button>
        </div>
      )}
      {query.data && !query.data.storage_available && (
        <div className="integration-feedback" role="alert">
          Сохранение ключей пока не настроено на сервере. Попросите
          администратора включить защищённое хранилище.
        </div>
      )}
      <label className="integration-field">
        Название подключения
        <input
          value={connectionName}
          maxLength={120}
          onChange={(e) => setConnectionName(e.target.value)}
          placeholder={
            scope.provider === "moysklad"
              ? "Основной склад"
              : "Мой магазин Ozon"
          }
        />
      </label>
      {scope.provider === "ozon" && (
        <label className="integration-field">
          ID клиента Ozon
          <input
            value={account}
            maxLength={256}
            disabled={!!metadata?.enabled}
            autoComplete="off"
            inputMode="numeric"
            onChange={(e) => setAccount(e.target.value.trim())}
            placeholder="Client ID из личного кабинета Ozon"
          />
        </label>
      )}
      <label className="integration-field">
        {metadata?.enabled ? "Новый ключ доступа" : "Ключ доступа"}
        <input
          ref={secret}
          type="password"
          maxLength={8192}
          autoComplete="new-password"
          spellCheck={false}
          onChange={(e) => setHasSecret(!!e.currentTarget.value)}
          placeholder={`Вставьте ключ из ${label}`}
        />
      </label>
      <a
        href={
          scope.provider === "moysklad"
            ? "https://dev.moysklad.ru/doc/api/remap/1.2/"
            : "https://docs.ozon.ru/api/seller/"
        }
        target="_blank"
        rel="noreferrer"
      >
        Где взять ключ?
      </a>
      <p>
        Мы проверим доступ и сохраним ключ в зашифрованном виде. Загрузка данных
        запускается отдельно.
      </p>
      <label className="integration-ack">
        <input
          type="checkbox"
          checked={ack}
          onChange={(e) => setAck(e.target.checked)}
        />
        Подтверждаю, что этот аккаунт принадлежит текущему рабочему
        пространству.
      </label>
      {localError && <p role="alert">{localError}</p>}
      {(samePending ||
        finished ||
        (submission.feedbackScope &&
          sameCredentialScope(submission.feedbackScope, {
            ...scope,
            account,
          }))) &&
        submission.message && (
          <p className="integration-feedback" role="alert">
            {submission.message}
          </p>
        )}
      {samePending && !submission.busy && (
        <div className="provider-actions">
          {!submission.sessionLost &&
            submission.code !== "IDEMPOTENCY_CONFLICT" && (
              <button className="secondary" onClick={submission.retry}>
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
      <div className="provider-actions">
        <button
          disabled={
            !query.data?.storage_available ||
            !!query.error ||
            !connectionName.trim() ||
            !hasSecret ||
            !ack ||
            !!submission.pending ||
            submission.busy ||
            (scope.provider === "ozon" && !account)
          }
          onClick={save}
        >
          {submission.busy ? "Проверяем…" : "Сохранить и проверить"}
        </button>
        <button className="secondary" onClick={onClose}>
          Отмена
        </button>
      </div>
      {metadata?.enabled && (
        <div className="settings-disconnect">
          {!disconnecting ? (
            <button
              className="text-action danger"
              onClick={() => setDisconnecting(true)}
            >
              Отключить {label}
            </button>
          ) : (
            <>
              <p>
                Новые загрузки и отправка остатков для этого подключения станут
                недоступны. История и загруженные данные сохранятся.
              </p>
              <label className="integration-ack">
                <input
                  type="checkbox"
                  checked={disconnectAck}
                  onChange={(e) => setDisconnectAck(e.target.checked)}
                />
                Подтверждаю отключение {label}
              </label>
              <button
                className="secondary"
                disabled={
                  !disconnectAck || !!submission.pending || submission.busy
                }
                onClick={() => submission.disconnect(scope, metadata.revision)}
              >
                Подтвердить отключение
              </button>
            </>
          )}
        </div>
      )}
      <details className="technical-details credential-details">
        <summary>Технические детали</summary>
        <dl>
          <div>
            <dt>Рабочее пространство</dt>
            <dd>
              <code>{scope.tenant}</code>
            </dd>
          </div>
          <div>
            <dt>Аккаунт</dt>
            <dd>
              <code>
                {metadata?.account ||
                  account ||
                  "Будет определён при проверке ключа"}
              </code>
            </dd>
          </div>
          {metadata && (
            <>
              <div>
                <dt>Версия настроек</dt>
                <dd>{metadata.revision}</dd>
              </div>
              <div>
                <dt>Последняя проверка</dt>
                <dd>{new Date(metadata.checked_at).toLocaleString("ru-RU")}</dd>
              </div>
            </>
          )}
        </dl>
        {accounts.length > 0 && (
          <>
            <h4>Добавленные аккаунты</h4>
            <ul className="technical-account-list">
              {accounts.map((item, index) => (
                <li key={item.account}>
                  <span>{item.name || `Аккаунт ${index + 1}`}</span>
                  <code>{item.account}</code>
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
                `tenant: ${scope.tenant}\naccount: ${metadata?.account || account}\n${accounts.map((item, index) => `account ${index + 1}: ${item.account}`).join("\n")}`,
              );
              setCopied(true);
            } catch {
              setLocalError("Выделите и скопируйте идентификаторы вручную.");
            }
          }}
        >
          {copied ? "Скопировано" : "Копировать идентификаторы"}
        </button>
        <p>
          Ключи доступа не отображаются в диагностике и не сохраняются в
          браузере.
        </p>
      </details>
    </div>
  );
}
