import { useState } from "react";
import { Link, useSearchParams, useNavigate } from "react-router-dom";
import { CredentialEditor } from "./CredentialEditor";
import { PageHeader, StatePanel } from "./UI";
import type { Account } from "./api";
import type { CredentialSubmissionState } from "./CredentialSubmission";
import type { ImportSubmissionState } from "./ImportSubmission";
import { ImportPanel } from "./ImportPanel";
import {
  MoySkladAccountGeneral,
  MoySkladAccountNavigation,
  MoySkladConnectionSummary,
  MoySkladProductSettings,
  MoySkladSettingsEvents,
  MoySkladWarehouseSettings,
} from "./MoySkladAccountSections";
import { MoySkladConnectionSettings } from "./MoySkladConnectionSettings";
import {
  useMoySkladSettings,
  useSettingsDraft,
  useSettingsLeaveGuard,
} from "./MoySkladSettingsData";
import "./moysklad-settings.css";

type Props = {
  tenant: string;
  account: string;
  accounts: Account[];
  accountsLoading: boolean;
  credentials: CredentialSubmissionState;
  imports: ImportSubmissionState;
};
export function MoySkladAccountSettingsPage(props: Props) {
  const navigate = useNavigate();
  const [query] = useSearchParams();
  const account = query.get("account") || props.account;
  if (props.accountsLoading)
    return <StatePanel kind="loading">Загружаем аккаунт МойСклад…</StatePanel>;
  if (!account)
    return (
      <div className="moy-settings-page">
        <Link className="moy-settings-back" to="/integrations">
          ← Назад к интеграциям
        </Link>
        <PageHeader
          title="Настройки аккаунта МойСклад"
          description="Подключите аккаунт, чтобы загружать данные в STYK."
        />
        <section className="moy-settings-card">
          <CredentialEditor
            scope={{ tenant: props.tenant, provider: "moysklad", account: "" }}
            registration="NOT_CONFIGURED"
            accounts={[]}
            submission={props.credentials}
            onClose={() => navigate("/integrations")}
          />
        </section>
      </div>
    );
  if (
    !account ||
    !props.accounts.some(
      (item) => item.source === "moysklad" && item.account === account,
    )
  )
    return (
      <>
        <PageHeader title="Настройки аккаунта МойСклад" />
        <StatePanel>
          <div>
            <h2>
              {account ? "Аккаунт недоступен" : "МойСклад ещё не подключён"}
            </h2>
            <p>
              {account
                ? "Выберите доступное подключение в разделе интеграций."
                : "Добавьте подключение МойСклад, чтобы настроить его."}
            </p>
            <Link to="/integrations">Открыть интеграции</Link>
          </div>
        </StatePanel>
      </>
    );
  return (
    <MoySkladSettingsContent
      key={`${props.tenant}:${account}`}
      {...props}
      account={account}
    />
  );
}
function MoySkladSettingsContent({
  tenant,
  account,
  credentials,
  imports,
}: Props) {
  const [section, setSection] = useState("general"),
    [tokenDirty, setTokenDirty] = useState(false);
  const data = useMoySkladSettings(tenant, account);
  const draft = useSettingsDraft(tenant, account, data.settings.data);
  useSettingsLeaveGuard(draft.dirty || tokenDirty);
  const meta = data.credential.data?.credential;
  const unavailable = data.credential.isError || data.connection.isError;
  const name = data.settings.data?.name || meta?.name || "Аккаунт МойСклад";
  const scope = { tenant, provider: "moysklad" as const, account };
  return (
    <div className="moy-settings-page">
      <Link to="/integrations" className="moy-settings-back">
        <span aria-hidden="true">←</span> Назад к интеграциям
      </Link>
      <PageHeader
        title="Настройки аккаунта МойСклад"
        description="Управляйте подключением и данными вашего аккаунта."
      />
      <div className="moy-settings-layout">
        <MoySkladAccountNavigation section={section} onSelect={setSection} />
        <div className="moy-settings-main">
          <div hidden={section !== "general"} className="moy-general-layout">
            {data.settings.isLoading ? (
              <StatePanel kind="loading">
                Загружаем основную информацию…
              </StatePanel>
            ) : data.settings.isError || !draft.values ? (
              <StatePanel kind="error">
                <div>
                  <p>Не удалось загрузить основную информацию.</p>
                  <button
                    className="secondary"
                    onClick={() => void data.settings.refetch()}
                  >
                    Повторить
                  </button>
                  <button
                    className="text-action"
                    onClick={() => setSection("connection")}
                  >
                    Перейти к замене токена
                  </button>
                </div>
              </StatePanel>
            ) : (
              <MoySkladAccountGeneral
                values={draft.values}
                edit={draft.edit}
                disabled={draft.busy || draft.unconfirmed}
              />
            )}
            <MoySkladConnectionSummary
              name={name}
              provider={data.provider}
              meta={meta}
              loading={data.credential.isLoading || data.connection.isLoading}
              error={unavailable}
            />
          </div>
          <div hidden={section !== "connection"}>
            <MoySkladConnectionSettings
              scope={scope}
              name={name}
              meta={meta}
              provider={data.provider}
              available={!!data.credential.data?.storage_available}
              failed={
                unavailable ||
                data.credential.isLoading ||
                data.connection.isLoading
              }
              submission={credentials}
              onRefresh={data.refresh}
              onDirty={setTokenDirty}
            />
          </div>
          <div hidden={section !== "sync"}>
            <section className="moy-settings-card">
              <header>
                <h2>Синхронизация</h2>
                <p>
                  Ручная загрузка товаров, вариантов, складов и остатков из
                  МойСклад.
                </p>
              </header>
              <div className="moy-settings-fields">
                <p className="moy-muted">
                  Загрузка читает данные из МойСклад. Заказы загружаются
                  отдельно. Расписание автоматической публикации настраивается в
                  разделе интеграций и здесь не изменяется.
                </p>
                <ImportPanel
                  scope={scope}
                  configured={!unavailable && !!meta?.enabled}
                  submission={imports}
                  latestBatch={data.provider?.import}
                  accountLabel={name}
                  onSettings={() => setSection("connection")}
                />
              </div>
            </section>
          </div>
          <div hidden={section !== "warehouses"}>
            {data.connection.isError ? (
              <StatePanel kind="error">
                Не удалось загрузить данные складов.{" "}
                <button
                  className="text-action"
                  onClick={() => void data.connection.refetch()}
                >
                  Повторить
                </button>
              </StatePanel>
            ) : (
              <MoySkladWarehouseSettings provider={data.provider} />
            )}
          </div>
          <div hidden={section !== "products"}>
            <MoySkladProductSettings />
          </div>
          <div hidden={section !== "events"}>
            <MoySkladSettingsEvents
              items={data.history.data?.items || []}
              loading={data.history.isLoading}
              error={data.history.isError}
              hasNext={!!data.history.data?.has_next}
              onRetry={() => void data.history.refetch()}
            />
          </div>
          {draft.values && (
            <div className="moy-settings-savebar">
              <div aria-live="polite">
                {draft.message ? (
                  <p role="status">{draft.message}</p>
                ) : (
                  <p>
                    {draft.dirty
                      ? "Есть несохранённые изменения"
                      : "Изменения названия и описания сохраняются отдельно от токена."}
                  </p>
                )}
                {!draft.valid && (
                  <small role="alert">
                    Введите название до 120 символов. Описание — до 1000
                    символов, без переносов строк.
                  </small>
                )}
              </div>
              <div className="moy-save-actions">
                {draft.unconfirmed ||
                draft.message.includes("другой вкладке") ? (
                  <button
                    className="secondary"
                    disabled={draft.busy}
                    onClick={() => void draft.inspect()}
                  >
                    Проверить сохранение
                  </button>
                ) : (
                  <button
                    className="secondary"
                    disabled={!draft.dirty || draft.busy}
                    onClick={draft.reset}
                  >
                    Отменить
                  </button>
                )}
                <button
                  disabled={
                    !draft.dirty ||
                    !draft.valid ||
                    draft.busy ||
                    draft.unconfirmed
                  }
                  onClick={() => void draft.save()}
                >
                  {draft.busy ? "Сохраняем…" : "Сохранить изменения"}
                </button>
              </div>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
