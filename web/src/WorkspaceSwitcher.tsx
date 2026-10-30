import type { Account } from "./api";

export type Marketplace = "wildberries" | "ozon" | "yandex";
export const marketplaceNames: Record<Marketplace, string> = {
  wildberries: "Wildberries",
  ozon: "Ozon",
  yandex: "Яндекс Маркет",
};
const marketplaces: { id: Marketplace; label: string }[] = [
  { id: "wildberries", label: "WB" },
  { id: "ozon", label: "OZON" },
  { id: "yandex", label: "ЯМ" },
];
export function WorkspaceSwitcher({
  marketplace,
  onMarketplaceChange,
  accounts,
  account,
  onAccountChange,
  onAdd,
  moyLabel,
  loading,
  error,
  addDisabled,
}: {
  marketplace: Marketplace;
  onMarketplaceChange: (marketplace: Marketplace) => void;
  accounts: Account[];
  account: string;
  onAccountChange: (account: string) => void;
  onAdd: () => void;
  moyLabel: string;
  loading: boolean;
  error: boolean;
  addDisabled: boolean;
}) {
  const supported = marketplace === "ozon";
  const available = accounts.filter((item) => item.source === marketplace);
  return (
    <section className="workspace-switcher" aria-label="Рабочее пространство">
      <div className="fixed-moy-account">
        <span className="fixed-moy-mark" aria-hidden="true">
          МС
        </span>
        <div>
          <span>МойСклад</span>
          <strong>{moyLabel}</strong>
        </div>
      </div>
      <div
        className="marketplace-options"
        role="group"
        aria-label="Маркетплейс"
      >
        {marketplaces.map((item) => (
          <button
            key={item.id}
            type="button"
            className={`marketplace-option ${item.id}`}
            aria-pressed={marketplace === item.id}
            onClick={() => onMarketplaceChange(item.id)}
          >
            {item.label}
          </button>
        ))}
      </div>
      <label className="marketplace-account-label">
        Аккаунт {marketplaceNames[marketplace]}
        <select
          aria-label={marketplaceNames[marketplace]}
          value={supported ? account : ""}
          onChange={(event) => onAccountChange(event.target.value)}
          disabled={!supported || loading || error}
        >
          <option value="">
            {loading
              ? "Загрузка аккаунтов…"
              : available.length
                ? "Выберите аккаунт"
                : "Нет подключённых аккаунтов"}
          </option>
          {supported &&
            available.map((item, index) => (
              <option key={item.account} value={item.account}>
                {item.name ||
                  (item.account.startsWith("mock-")
                    ? "Тестовый аккаунт (мок)"
                    : `Аккаунт ${index + 1}`)}
              </option>
            ))}
        </select>
      </label>
      <button
        className="add-marketplace-account secondary"
        type="button"
        disabled={!supported || loading || error || addDisabled}
        onClick={onAdd}
      >
        <span aria-hidden="true">+</span> Добавить аккаунт
      </button>
      {!supported && (
        <p className="marketplace-unavailable">
          Подключение {marketplaceNames[marketplace]} пока недоступно.
        </p>
      )}
      {error && (
        <p className="marketplace-unavailable" role="alert">
          Не удалось загрузить аккаунты. Обновите страницу.
        </p>
      )}
    </section>
  );
}
