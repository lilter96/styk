import { useState, type FormEvent } from "react";
import { ApiError, post } from "./api";
import {
  marketplacePlanName,
  marketplaceQuotaMessages,
  type MarketplacePlan,
} from "./marketplacePlan";
import { PageHeader } from "./UI";
import "./profile-settings.css";

const providerNames: Record<string, string> = {
  ozon: "Ozon",
  wildberries: "Wildberries",
  yandex: "Яндекс Маркет",
};

function passwordError(error: unknown) {
  if (error instanceof ApiError) {
    if (error.code === "INVALID_CURRENT_PASSWORD")
      return "Текущий пароль указан неверно.";
    if (error.code === "INVALID_PASSWORD")
      return "Новый пароль должен содержать от 12 до 72 байт.";
    if (error.status === 429)
      return "Слишком много попыток. Попробуйте позже.";
    if (error.status === 403)
      return "Действие недоступно в текущем режиме.";
  }
  return "Не удалось изменить пароль. Попробуйте позже.";
}

function PasswordForm({ readOnly }: { readOnly?: boolean }) {
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");

  async function submit(event: FormEvent) {
    event.preventDefault();
    setMessage("");
    setError("");
    const size = new TextEncoder().encode(next).length;
    if (size < 12 || size > 72) {
      setError("Новый пароль должен содержать от 12 до 72 байт.");
      return;
    }
    if (next !== confirmation) {
      setError("Новый пароль и подтверждение не совпадают.");
      return;
    }
    setBusy(true);
    try {
      await post<null>(
        "/api/profile/password",
        { current_password: current, new_password: next },
        AbortSignal.timeout(30000),
      );
      setCurrent("");
      setNext("");
      setConfirmation("");
      setMessage("Пароль изменён. Другие активные сеансы завершены.");
    } catch (cause) {
      setError(passwordError(cause));
    } finally {
      setBusy(false);
    }
  }

  if (readOnly)
    return (
      <p className="profile-readonly">
        В режиме поддержки изменение пароля недоступно.
      </p>
    );

  return (
    <form className="profile-password-form" onSubmit={(event) => void submit(event)}>
      <label htmlFor="profile-current-password">Текущий пароль</label>
      <input
        id="profile-current-password"
        type="password"
        autoComplete="current-password"
        value={current}
        onChange={(event) => setCurrent(event.target.value)}
        required
      />
      <label htmlFor="profile-new-password">Новый пароль</label>
      <input
        id="profile-new-password"
        type="password"
        autoComplete="new-password"
        minLength={12}
        value={next}
        onChange={(event) => setNext(event.target.value)}
        required
      />
      <label htmlFor="profile-confirm-password">Повторите новый пароль</label>
      <input
        id="profile-confirm-password"
        type="password"
        autoComplete="new-password"
        minLength={12}
        value={confirmation}
        onChange={(event) => setConfirmation(event.target.value)}
        required
      />
      <small>Не менее 12 символов и не более 72 байт.</small>
      <button type="submit" disabled={busy}>
        {busy ? "Сохраняем…" : "Сохранить пароль"}
      </button>
      {message && (
        <p className="profile-message" role="status">
          {message}
        </p>
      )}
      {error && (
        <p className="profile-error" role="alert">
          {error}
        </p>
      )}
    </form>
  );
}

function PlanDetails({ plan }: { plan: MarketplacePlan }) {
  const providers = Object.entries(plan.providers).sort(([left], [right]) =>
    (providerNames[left] ?? left).localeCompare(
      providerNames[right] ?? right,
      "ru",
    ),
  );
  return (
    <div className="profile-plan-details">
      <div className="profile-plan-summary">
        <strong>{marketplacePlanName(plan.code)}</strong>
        <span>
          {plan.used} из {plan.total_limit}
        </span>
      </div>
      <dl className="profile-plan-facts">
        <div>
          <dt>Всего аккаунтов</dt>
          <dd>{plan.total_limit}</dd>
        </div>
        <div>
          <dt>На одну площадку</dt>
          <dd>{plan.provider_limit}</dd>
        </div>
        <div>
          <dt>Использовано</dt>
          <dd>{plan.used}</dd>
        </div>
      </dl>
      {providers.length > 0 && (
        <ul className="profile-plan-providers" aria-label="Аккаунты по площадкам">
          {providers.map(([provider, used]) => (
            <li key={provider}>
              <span>{providerNames[provider] ?? provider}</span>
              <strong>{used}</strong>
            </li>
          ))}
        </ul>
      )}
      {plan.exceeded && (
        <p className="profile-plan-warning" role="alert">
          {marketplaceQuotaMessages.PLAN_OVER_LIMIT}
        </p>
      )}
    </div>
  );
}

function PlanCard({
  plan,
  loading,
  unavailable,
  onReload,
}: {
  plan?: MarketplacePlan;
  loading: boolean;
  unavailable: boolean;
  onReload?: () => void;
}) {
  return (
    <section className="profile-card profile-plan-card" aria-labelledby="profile-plan-title">
      <div className="profile-card-heading">
        <div>
          <h2 id="profile-plan-title">Тариф</h2>
          <p>Лимиты аккаунтов маркетплейсов для этого рабочего пространства.</p>
        </div>
        {onReload && (
          <button type="button" className="text-action" onClick={onReload}>
            Обновить
          </button>
        )}
      </div>
      {unavailable ? (
        <p className="profile-error" role="alert">
          Не удалось загрузить данные тарифа. Повторите попытку позже.
        </p>
      ) : plan ? (
        <PlanDetails plan={plan} />
      ) : loading ? (
        <p className="profile-muted" role="status">
          Загружаем тариф…
        </p>
      ) : (
        <p className="profile-muted">
          Тариф станет доступен после подключения рабочего пространства.
        </p>
      )}
    </section>
  );
}

export function ProfileSettings({
  email,
  plan,
  planLoading = false,
  planUnavailable = false,
  onReloadPlan,
  readOnly = false,
}: {
  email: string;
  plan?: MarketplacePlan;
  planLoading?: boolean;
  planUnavailable?: boolean;
  onReloadPlan?: () => void;
  readOnly?: boolean;
}) {
  return (
    <div className="profile-settings">
      <PageHeader
        title="Настройки профиля"
        description="Управляйте доступом к аккаунту и проверяйте лимиты тарифа."
      />
      <div className="profile-settings-grid">
        <div className="profile-settings-main">
          <section className="profile-card profile-account-card" aria-labelledby="profile-account-title">
            <div className="profile-avatar" aria-hidden="true">
              {email.slice(0, 1).toUpperCase() || "S"}
            </div>
            <div>
              <h2 id="profile-account-title">Аккаунт</h2>
              <p className="profile-email">{email}</p>
              <p className="profile-muted">
                Электронная почта используется для входа и восстановления
                доступа.
              </p>
            </div>
          </section>
          <section className="profile-card" aria-labelledby="profile-password-title">
            <div className="profile-card-heading">
              <div>
                <h2 id="profile-password-title">Смена пароля</h2>
                <p>
                  После смены пароля другие активные сеансы будут завершены.
                </p>
              </div>
            </div>
            <PasswordForm readOnly={readOnly} />
          </section>
        </div>
        <PlanCard
          plan={plan}
          loading={planLoading}
          unavailable={planUnavailable}
          onReload={onReloadPlan}
        />
      </div>
    </div>
  );
}
