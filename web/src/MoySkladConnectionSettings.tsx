import { useEffect, useRef, useState } from "react";
import { ApiError, post } from "./api";
import { IntegrationDialog } from "./IntegrationDialog";
import type {
  CredentialMetadata,
  CredentialScope,
  CredentialSubmissionState,
} from "./CredentialSubmission";
import {
  connectionState,
  probeMessage,
  settingsDate,
  successfulCheck,
  type MoyProvider,
} from "./MoySkladSettingsData";

export function MoySkladConnectionSettings({
  scope,
  name,
  meta,
  provider,
  available,
  failed,
  submission,
  onRefresh,
  onDirty,
}: {
  scope: CredentialScope;
  name: string;
  meta?: CredentialMetadata | null;
  provider?: MoyProvider;
  available: boolean;
  failed: boolean;
  submission: CredentialSubmissionState;
  onRefresh: () => Promise<void>;
  onDirty: (dirty: boolean) => void;
}) {
  const [checking, setChecking] = useState(false),
    [feedback, setFeedback] = useState(""),
    [disconnectOpen, setDisconnectOpen] = useState(false),
    [ack, setAck] = useState(false);
  const lock = useRef(false);
  const status = connectionState(provider, meta);
  async function probe() {
    if (lock.current || submission.pending) return;
    lock.current = true;
    setChecking(true);
    setFeedback("");
    try {
      const result = await post<{ status: string; reason?: string }>(
        "/api/integrations/moysklad/probe",
        { tenant: scope.tenant, account: scope.account },
        AbortSignal.timeout(15000),
      );
      setFeedback(probeMessage(result));
      await onRefresh();
    } catch (error) {
      setFeedback(
        probeMessage({
          reason: error instanceof ApiError ? error.code : undefined,
        }),
      );
    } finally {
      lock.current = false;
      setChecking(false);
    }
  }
  return (
    <section className="moy-settings-card">
      <header>
        <h2>Подключение к МойСклад</h2>
        <p>Управляйте доступом к данным вашего аккаунта.</p>
      </header>
      <div className="moy-settings-fields">
        <dl className="moy-facts">
          <div>
            <dt>Наименование подключения</dt>
            <dd>{name}</dd>
          </div>
          <div>
            <dt>Статус</dt>
            <dd>
              <span className={`moy-status ${status.tone}`}>
                {failed ? "Состояние недоступно" : status.text}
              </span>
            </dd>
          </div>
          <div>
            <dt>Последняя успешная проверка</dt>
            <dd>{settingsDate(successfulCheck(provider, meta))}</dd>
          </div>
          <div>
            <dt>Последняя загрузка</dt>
            <dd>{settingsDate(provider?.import?.observed_at)}</dd>
          </div>
        </dl>
        <div>
          <button
            className="secondary"
            disabled={
              checking ||
              !!submission.pending ||
              failed ||
              !(meta?.enabled || provider?.status === "CONFIGURED_NOT_VERIFIED")
            }
            onClick={() => void probe()}
          >
            {checking ? "Проверяем подключение…" : "Проверить подключение"}
          </button>
        </div>
        {feedback && (
          <p className="moy-feedback" role="status">
            {feedback}
          </p>
        )}
        <MoySkladTokenReplacement
          scope={scope}
          name={name}
          meta={meta}
          available={available && !failed}
          submission={submission}
          onDirty={onDirty}
        />
      </div>
      <footer className="moy-disconnect">
        <div>
          <strong>Отключение аккаунта</strong>
          <p>История, товары и сопоставления сохранятся.</p>
        </div>
        <button
          className="secondary danger"
          disabled={!meta?.enabled || !!submission.pending || failed}
          onClick={() => {
            setAck(false);
            setDisconnectOpen(true);
          }}
        >
          Отключить аккаунт
        </button>
      </footer>
      {disconnectOpen && (
        <IntegrationDialog
          title="Отключить аккаунт МойСклад?"
          onClose={() => setDisconnectOpen(false)}
        >
          <div className="moy-settings-fields">
            <p>
              Новые загрузки и отправка данных с этим токеном станут недоступны.
              Уже отправленный внешний запрос может завершиться. Загруженные
              данные и история останутся в STYK.
            </p>
            <p>
              Для повторного подключения потребуется сохранить и проверить
              действующий токен.
            </p>
            <label className="integration-ack">
              <input
                type="checkbox"
                checked={ack}
                onChange={(event) => setAck(event.target.checked)}
              />
              Подтверждаю отключение аккаунта
            </label>
            <div className="provider-actions">
              <button
                className="secondary"
                onClick={() => setDisconnectOpen(false)}
              >
                Отмена
              </button>
              <button
                disabled={!ack || !meta || !!submission.pending}
                onClick={() => {
                  if (meta) {
                    submission.disconnect(scope, meta.revision);
                    setDisconnectOpen(false);
                  }
                }}
              >
                Подтвердить отключение
              </button>
            </div>
          </div>
        </IntegrationDialog>
      )}
    </section>
  );
}
export function MoySkladTokenReplacement({
  scope,
  name,
  meta,
  available,
  submission,
  onDirty,
}: {
  scope: CredentialScope;
  name: string;
  meta?: CredentialMetadata | null;
  available: boolean;
  submission: CredentialSubmissionState;
  onDirty: (dirty: boolean) => void;
}) {
  const input = useRef<HTMLInputElement>(null);
  const [open, setOpen] = useState(false),
    [hasToken, setHasToken] = useState(false),
    [ack, setAck] = useState(false),
    [error, setError] = useState("");
  useEffect(() => {
    onDirty(hasToken);
    return () => onDirty(false);
  }, [hasToken, onDirty]);
  useEffect(() => {
    if (
      submission.result?.tenant === scope.tenant &&
      submission.result.account === scope.account
    ) {
      if (input.current) input.current.value = "";
      setHasToken(false);
      setAck(false);
      setOpen(false);
    }
  }, [submission.result, scope.tenant, scope.account]);
  function save() {
    const token = input.current?.value;
    if (!token || !ack || submission.pending) return;
    if (
      token.trim() !== token ||
      ["\r", "\n", "\0"].some((char) => token.includes(char))
    ) {
      setError("Уберите пробелы и переносы строк из токена.");
      return;
    }
    try {
      submission.start(scope, {
        name,
        secret: token,
        expectedRevision: meta?.revision || 0,
      });
      if (input.current) input.current.value = "";
      setHasToken(false);
      setAck(false);
      setError("");
    } catch {
      setError("Не удалось подготовить защищённый запрос. Токен не отправлен.");
    }
  }
  return (
    <div className="moy-token">
      {!submission.pending &&
        !submission.result &&
        submission.message &&
        submission.feedbackScope?.tenant === scope.tenant &&
        submission.feedbackScope.account === scope.account &&
        submission.feedbackScope.provider === scope.provider && (
          <p className="moy-feedback" role="alert">
            {submission.message}
          </p>
        )}
      <div className="moy-token-heading">
        <div>
          <h3>API-токен</h3>
          <p>
            {meta?.enabled
              ? "Токен сохранён"
              : "Токен не сохранён. Добавьте его для доступа к МойСклад."}
          </p>
        </div>
        {!open && (
          <button
            className="secondary"
            disabled={!available || !!submission.pending}
            onClick={() => setOpen(true)}
          >
            {meta?.enabled ? "Заменить токен" : "Добавить токен"}
          </button>
        )}
      </div>
      {open && (
        <div className="moy-token-form">
          <label className="integration-field">
            Новый API-токен
            <input
              ref={input}
              type="password"
              autoComplete="new-password"
              maxLength={8192}
              spellCheck={false}
              onChange={(event) => {
                setHasToken(!!event.target.value);
                setError("");
              }}
            />
          </label>
          <p>
            Проверим принадлежность токена этому аккаунту и сохраним его в
            зашифрованном виде. Загрузка данных запускается отдельно.
          </p>
          <label className="integration-ack">
            <input
              type="checkbox"
              checked={ack}
              onChange={(event) => setAck(event.target.checked)}
            />
            Подтверждаю, что аккаунт принадлежит мне
          </label>
          {error && <p role="alert">{error}</p>}
          <div className="provider-actions">
            <button
              disabled={!hasToken || !ack || !!submission.pending || !available}
              onClick={save}
            >
              {submission.busy
                ? "Проверяем и сохраняем…"
                : "Сохранить и проверить токен"}
            </button>
            <button
              className="secondary"
              onClick={() => {
                if (input.current) input.current.value = "";
                setHasToken(false);
                setOpen(false);
                setAck(false);
              }}
            >
              Отмена
            </button>
          </div>
        </div>
      )}
      {!available && (
        <p className="moy-muted">
          Изменение токена сейчас недоступно. Обновите состояние подключения.
        </p>
      )}
    </div>
  );
}
