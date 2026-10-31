import moyskladLogo from "./assets/brands/moysklad.png";
import { Link } from "react-router-dom";
import { Icon, type IconName } from "./UI";
import {
  settingsDate,
  successfulCheck,
  connectionState,
  importStates,
  type MoyProvider,
  type Settings,
} from "./MoySkladSettingsData";
import type { CredentialMetadata } from "./CredentialSubmission";
import type { ImportOperation } from "./api";

export const moySections: { id: string; label: string; icon: IconName }[] = [
  { id: "general", label: "Основная информация", icon: "overview" },
  { id: "connection", label: "Подключение", icon: "integrations" },
  { id: "sync", label: "Синхронизация", icon: "arrow" },
  { id: "warehouses", label: "Склады и остатки", icon: "warehouse" },
  { id: "products", label: "Сопоставление товаров", icon: "products" },
  { id: "events", label: "Журнал загрузок", icon: "diagnostics" },
];
export function MoySkladAccountNavigation({
  section,
  onSelect,
}: {
  section: string;
  onSelect: (section: string) => void;
}) {
  return (
    <nav className="moy-settings-nav" aria-label="Настройки МойСклад">
      {moySections.map((item) => (
        <button
          className="moy-nav-item"
          type="button"
          key={item.id}
          aria-current={item.id === section ? "page" : undefined}
          onClick={() => onSelect(item.id)}
        >
          <Icon name={item.icon} />
          <span>{item.label}</span>
        </button>
      ))}
    </nav>
  );
}
export function MoySkladAccountGeneral({
  values,
  edit,
  disabled,
}: {
  values: Settings;
  edit: (field: "name" | "description", value: string) => void;
  disabled: boolean;
}) {
  return (
    <section className="moy-settings-card">
      <header>
        <h2>Основная информация</h2>
        <p>Название и описание аккаунта для вашей команды.</p>
      </header>
      <div className="moy-settings-fields">
        <label htmlFor="moy-account-name">
          Название аккаунта <span aria-hidden="true">*</span>
        </label>
        <input
          id="moy-account-name"
          value={values.name}
          maxLength={120}
          disabled={disabled}
          onChange={(event) => edit("name", event.target.value)}
          placeholder="Основной аккаунт"
          autoComplete="off"
        />
        <small>Отображается в боковом меню STYK.</small>
        <label htmlFor="moy-account-description">
          Описание <span className="moy-optional">Необязательно</span>
        </label>
        <textarea
          id="moy-account-description"
          rows={4}
          maxLength={1000}
          disabled={disabled}
          value={values.description}
          onChange={(event) => edit("description", event.target.value)}
          placeholder="Например, какой склад или направление использует этот аккаунт"
        />
        <small>
          Заметка о назначении подключения. Не указывайте ключи доступа.
        </small>
      </div>
      <footer className="moy-card-note">
        <Icon name="integrations" />
        <p>
          Эти данные видны только в STYK. Настройки организации в МойСклад не
          изменятся.
        </p>
      </footer>
    </section>
  );
}
export function MoySkladConnectionSummary({
  name,
  provider,
  meta,
  loading,
  error,
}: {
  name: string;
  provider?: MoyProvider;
  meta?: CredentialMetadata | null;
  loading: boolean;
  error: boolean;
}) {
  const status = connectionState(provider, meta);
  return (
    <aside className="moy-settings-card moy-connection-summary">
      <div className="moy-summary-brand">
        <img src={moyskladLogo} alt="" />
        <div>
          <h2>МойСклад</h2>
          <p>{name}</p>
        </div>
      </div>
      <span className={`moy-status ${status.tone}`}>
        {loading
          ? "Проверяем состояние…"
          : error
            ? "Состояние недоступно"
            : status.text}
      </span>
      <dl>
        <div>
          <dt>Токен доступа</dt>
          <dd>
            {loading
              ? "Загружаем…"
              : error
                ? "Не удалось загрузить"
                : meta?.enabled
                  ? "Токен сохранён"
                  : "Не сохранён"}
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
      <div className="moy-summary-note">
        <Icon name="diagnostics" />
        <p>Токен хранится в зашифрованном виде.</p>
      </div>
    </aside>
  );
}
export function MoySkladWarehouseSettings({
  provider,
}: {
  provider?: MoyProvider;
}) {
  const counts = provider?.import?.counts;
  return (
    <section className="moy-settings-card">
      <header>
        <h2>Склады и остатки</h2>
        <p>Данные последней завершённой загрузки из МойСклад.</p>
      </header>
      <div className="moy-settings-fields">
        <dl className="moy-facts">
          <div>
            <dt>Загрузка складов</dt>
            <dd>{settingsDate(provider?.import?.observed_at)}</dd>
          </div>
          <div>
            <dt>Импортировано складов</dt>
            <dd>
              {counts?.warehouses?.toLocaleString("ru-RU") ?? "Нет данных"}
            </dd>
          </div>
          <div>
            <dt>Записей остатков по складам</dt>
            <dd>
              {counts?.stock_rows?.toLocaleString("ru-RU") ?? "Нет данных"}
            </dd>
          </div>
        </dl>
        <p className="moy-muted">
          Сопоставление складов и просмотр остатков доступны в рабочем разделе.
        </p>
        <Link className="secondary moy-navigation-card" to="/warehouses">
          <Icon name="warehouse" />
          Открыть склады и сопоставления
          <Icon name="arrow" />
        </Link>
      </div>
    </section>
  );
}
export function MoySkladProductSettings() {
  return (
    <section className="moy-settings-card">
      <header>
        <h2>Сопоставление товаров</h2>
        <p>Работайте с товарами в соответствующих разделах STYK.</p>
      </header>
      <div className="moy-settings-fields">
        <Link className="secondary moy-navigation-card" to="/products">
          <Icon name="products" />
          Перейти к сопоставлению товаров
          <Icon name="arrow" />
        </Link>
        <Link className="secondary moy-navigation-card" to="/catalog-sync">
          <Icon name="catalog" />
          Открыть каталог
          <Icon name="arrow" />
        </Link>
      </div>
    </section>
  );
}
export function MoySkladSettingsEvents({
  items,
  loading,
  error,
  hasNext,
  onRetry,
}: {
  items: ImportOperation[];
  loading: boolean;
  error: boolean;
  hasNext: boolean;
  onRetry: () => void;
}) {
  return (
    <section className="moy-settings-card">
      <header>
        <h2>Журнал загрузок</h2>
        <p>Последние операции для этого подключения МойСклад.</p>
      </header>
      <div className="moy-settings-fields">
        {loading ? (
          <p role="status">Загружаем события…</p>
        ) : error ? (
          <p role="alert">
            Не удалось загрузить журнал.{" "}
            <button className="text-action" onClick={onRetry}>
              Повторить
            </button>
          </p>
        ) : items.length ? (
          <ol className="moy-events">
            {items.map((item) => (
              <li key={item.id}>
                <span className={`moy-event-dot ${item.state.toLowerCase()}`} />
                <div>
                  <strong>Загрузка каталога и остатков</strong>
                  <p>{importStates[item.state] || "Статус недоступен"}</p>
                </div>
                <time dateTime={item.finished_at || item.created_at}>
                  {settingsDate(item.finished_at || item.created_at)}
                </time>
              </li>
            ))}
          </ol>
        ) : (
          <p className="moy-muted">Загрузок для этого аккаунта пока нет.</p>
        )}
        {hasNext && (
          <Link to="/integrations">Открыть полную историю обновлений</Link>
        )}
      </div>
    </section>
  );
}
