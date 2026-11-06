import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type CSSProperties,
  type ReactNode,
} from "react";
import { ProviderLogo } from "./ProviderLogo";
import { Icon } from "./UI";
import { brandRegistry } from "./brands";

export type ProviderName = "moysklad" | "ozon" | "wildberries" | "yandex";
export const providerNames: Record<ProviderName, string> = {
  moysklad: brandRegistry.moysklad.label,
  ozon: brandRegistry.ozon.label,
  wildberries: brandRegistry.wildberries.label,
  yandex: brandRegistry["yandex-market"].label,
};
export function IntegrationProviderSection({
  provider,
  items: allItems,
  onAdd,
  addingDisabled = false,
  quotaReason,
  unavailable = false,
}: {
  provider: ProviderName;
  items: { id: string; name?: string; content: ReactNode }[];
  onAdd?: () => void;
  addingDisabled?: boolean;
  quotaReason?: string;
  unavailable?: boolean;
}) {
  const viewport = useRef<HTMLDivElement>(null);
  const [filter, setFilter] = useState("");
  const items = allItems.filter(
    (item) =>
      !filter ||
      (item.name || "")
        .toLocaleLowerCase("ru-RU")
        .includes(filter.toLocaleLowerCase("ru-RU")),
  );
  const [layout, setLayout] = useState({ perPage: 1, page: 0, pages: 1 });
  const name = providerNames[provider];
  const measure = useCallback(() => {
    const node = viewport.current;
    if (!node) return;
    const perPage = Math.min(
      4,
      Math.max(1, Math.floor((node.clientWidth + 12) / 312)),
    );
    const pages = Math.max(1, Math.ceil(items.length / perPage));
    const max = node.scrollWidth - node.clientWidth;
    const page =
      max > 1 && node.scrollLeft >= max - 2
        ? pages - 1
        : Math.min(
            pages - 1,
            Math.round(node.scrollLeft / (node.clientWidth + 12)),
          );
    setLayout((old) =>
      old.perPage === perPage && old.page === page && old.pages === pages
        ? old
        : { perPage, page, pages },
    );
  }, [items.length]);
  useEffect(() => {
    const node = viewport.current;
    if (!node) return;
    if (typeof ResizeObserver === "undefined") {
      measure();
      return;
    }
    const observer = new ResizeObserver(measure);
    observer.observe(node);
    measure();
    return () => observer.disconnect();
  }, [measure]);
  useEffect(() => {
    measure();
  }, [layout.perPage, measure]);
  useEffect(() => {
    viewport.current?.scrollTo?.({ left: 0, behavior: "instant" });
    measure();
  }, [items.map((item) => item.id).join("|"), measure]);
  function go(page: number) {
    const node = viewport.current;
    if (!node) return;
    const reduced = window.matchMedia?.(
      "(prefers-reduced-motion: reduce)",
    ).matches;
    node.scrollTo({
      left: Math.min(
        node.scrollWidth - node.clientWidth,
        Math.max(0, page * (node.clientWidth + 12)),
      ),
      behavior: reduced ? "instant" : "smooth",
    });
  }
  return (
    <section
      className={`integration-provider-section ${unavailable ? "unavailable" : ""}`}
      aria-label={`${name}: аккаунты`}
      data-provider-group={provider}
    >
      <header className="integration-provider-header">
        <div className="provider-group-identity">
          <ProviderLogo provider={provider} />
          <h2>{name}</h2>
          <span className="provider-account-count">
            {allItems.length}{" "}
            {allItems.length === 1
              ? "аккаунт"
              : allItems.length > 1 && allItems.length < 5
                ? "аккаунта"
                : "аккаунтов"}
          </span>
        </div>
        <div className="provider-group-controls">
          <button
            className="secondary provider-add"
            disabled={unavailable || addingDisabled || !!quotaReason || !onAdd}
            onClick={onAdd}
            title={
              unavailable ? "Подключение пока не поддерживается" : undefined
            }
          >
            <Icon name="plus" size="button" /> Добавить аккаунт
          </button>
          {layout.pages > 1 && (
            <div
              className="provider-carousel-controls"
              role="group"
              aria-label={`${name}: страницы аккаунтов`}
            >
              <button
                className="secondary"
                aria-label={`${name}: предыдущая страница`}
                disabled={layout.page === 0}
                onClick={() => go(layout.page - 1)}
              >
                <Icon name="chevron-left" size="button" />
              </button>
              <output aria-live="polite">
                {layout.page + 1} / {layout.pages}
              </output>
              <button
                className="secondary"
                aria-label={`${name}: следующая страница`}
                disabled={layout.page === layout.pages - 1}
                onClick={() => go(layout.page + 1)}
              >
                <Icon name="chevron-right" size="button" />
              </button>
            </div>
          )}
        </div>
      </header>
      {quotaReason && <p className="provider-quota-note">{quotaReason}</p>}
      {allItems.length >= 8 && (
        <label className="provider-account-search">
          <input
            aria-label={`${name}: поиск аккаунта`}
            type="search"
            placeholder="Поиск аккаунта"
            value={filter}
            onChange={(event) => setFilter(event.target.value)}
          />
        </label>
      )}
      {items.length ? (
        <div
          ref={viewport}
          className="integration-account-carousel"
          role="region"
          aria-label={`${name}: список аккаунтов`}
          tabIndex={0}
          onScroll={measure}
          onKeyDown={(event) => {
            if (event.target !== event.currentTarget) return;
            const directions: Record<string, number> = {
              ArrowLeft: layout.page - 1,
              ArrowRight: layout.page + 1,
              Home: 0,
              End: layout.pages - 1,
            };
            if (event.key in directions) {
              event.preventDefault();
              go(directions[event.key]);
            }
          }}
          style={{ "--cards-per-page": layout.perPage } as CSSProperties}
        >
          {items.map((item) => (
            <div className="integration-account-slot" key={item.id}>
              {item.content}
            </div>
          ))}
        </div>
      ) : (
        <div className="provider-group-empty">
          <Icon name="integrations" />
          <p>
            {unavailable
              ? "Интеграция пока недоступна. Подключённых аккаунтов нет."
              : filter
                ? "Аккаунтов с таким названием не найдено."
                : "Подключённых аккаунтов пока нет. Добавьте аккаунт, чтобы загружать данные."}
          </p>
        </div>
      )}
    </section>
  );
}
