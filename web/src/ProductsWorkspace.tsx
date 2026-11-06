import { useRef, useState } from "react";
import { Link, NavLink, useLocation } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { api, params, type Report } from "./api";
import type { Confirmation, Scope } from "./Confirmation";
import { ProductMappingPage } from "./ProductMappingPage";
import { ProductAutoMatching } from "./ProductAutoMatching";
import { ProductImport } from "./ProductImport";
import { CatalogSync } from "./CatalogSync";
import { CatalogSyncOperations } from "./CatalogSyncOperations";
import { BrandLogo } from "./BrandLogo";
import { Icon, StatePanel } from "./UI";
import {
  catalogURL,
  normalizeConfig,
  scopeBody,
  type CatalogConfig,
} from "./CatalogSyncTypes";
import "./products-workspace.css";

export function ProductsWorkspace({
  scope,
  confirmation,
  moyLabel,
  ozonLabel,
}: {
  scope: Scope;
  confirmation?: Confirmation;
  moyLabel?: string;
  ozonLabel?: string;
}) {
  const { pathname } = useLocation();
  const ready = Boolean(scope.tenant && scope.moy && scope.ozon);
  const report = useQuery({
    queryKey: ["diagnostics", scope],
    enabled: ready,
    queryFn: ({ signal }) =>
      api<Report>(`/api/diagnostics?${params(scopeBody(scope))}`, signal),
  });
  const config = useQuery({
    queryKey: ["catalog-config", scope],
    enabled: ready,
    queryFn: async ({ signal }) =>
      normalizeConfig(
        await api<CatalogConfig>(catalogURL("config", scope), signal),
      ),
    staleTime: Infinity,
    refetchOnWindowFocus: false,
    retry: false,
  });
  const route = pathname.replace(/^\/products\/?/, "");
  if (
    ![
      "",
      "rules",
      "history",
      "transfer",
      "import",
      "matching",
      "import/ozon-to-moysklad",
      "import/moysklad-to-ozon",
    ].includes(route)
  )
    return (
      <StatePanel>
        <h1>Страница не найдена</h1>
        <Link to="/products">Открыть товары</Link>
      </StatePanel>
    );
  const importPage = route.startsWith("import");
  const matchingPage = route === "matching";
  const title = matchingPage
    ? "Автосопоставление товаров"
    : route === "import"
      ? "Импорт товаров"
      : route === "import/ozon-to-moysklad"
        ? "Из Ozon в МойСклад"
        : route === "import/moysklad-to-ozon"
          ? "Из МойСклад в Ozon"
          : "Товары";
  return (
    <div className="products-workspace">
      {route && (
        <nav className="products-breadcrumb" aria-label="Путь к разделу">
          <Link to="/products">Товары</Link>
          <span>/</span>
          <span>
            {matchingPage
              ? "Автосопоставление"
              : importPage
                ? "Импорт"
                : route === "rules"
                  ? "Правила синхронизации"
                  : route === "history"
                    ? "История отправок"
                    : "Проверка изменений"}
          </span>
        </nav>
      )}
      <header className="products-heading">
        <h1>{title}</h1>
        <p>
          {importPage
            ? "Переносите товары между сервисами. Перед созданием проверим соответствия и обязательные поля."
            : "Управляйте соответствиями и изменениями товаров в одном месте"}
        </p>
      </header>
      <div className="products-account-pair">
        <span>
          <BrandLogo provider="moysklad" decorative />
          МойСклад
          {moyLabel && (
            <>
              : <b>{moyLabel}</b>
            </>
          )}
        </span>
        <span aria-hidden="true">↔</span>
        <span>
          <BrandLogo provider="ozon" decorative />
          Ozon
          {ozonLabel && (
            <>
              : <b>{ozonLabel}</b>
            </>
          )}
        </span>
      </div>
      {!ready ? (
        <StatePanel>
          Подключите и выберите аккаунты МойСклад и Ozon в разделе{" "}
          <Link to="/integrations">«Интеграции»</Link>.
        </StatePanel>
      ) : (
        <>
          {!importPage && !matchingPage && (
            <ProductsSummary report={report.data} config={config.data} />
          )}
          {!importPage && !matchingPage && (
            <div className="products-toolbar">
              <nav className="products-tabs" aria-label="Товары">
                <NavLink end to="/products">
                  Все товары
                </NavLink>
                <NavLink to="/products/rules">Правила синхронизации</NavLink>
                <NavLink to="/products/history">История отправок</NavLink>
              </nav>
              <div className="products-actions">
                <Link className="secondary" to="/products/matching">
                  <Icon name="search" size="button" />
                  Автосопоставление
                </Link>
                <ImportMenu />
                <Link className="text-action" to="/products/transfer">
                  Проверить изменения
                </Link>
              </div>
            </div>
          )}
          {route === "" && (
            <ProductMappingPage
              scope={scope}
              confirmation={confirmation}
              config={config.data}
            />
          )}
          {matchingPage && (
            <ProductAutoMatching scope={scope} confirmation={confirmation} />
          )}
          {route === "rules" && <CatalogSync scope={scope} view="rules" />}
          {route === "history" && (
            <div className="catalog-sync">
              <CatalogSyncOperations scope={scope} />
            </div>
          )}
          {route === "transfer" && (
            <CatalogSync scope={scope} view="transfer" />
          )}
          {route === "import" && <ProductImportHub />}
          {route === "import/ozon-to-moysklad" && (
            <ProductImport
              key={JSON.stringify([scope, "ozon"])}
              scope={scope}
              provider="ozon"
            />
          )}
          {route === "import/moysklad-to-ozon" && (
            <ProductImport
              key={JSON.stringify([scope, "moysklad"])}
              scope={scope}
              provider="moysklad"
            />
          )}
        </>
      )}
    </div>
  );
}

function ProductsSummary({
  report,
  config,
}: {
  report?: Report;
  config?: CatalogConfig;
}) {
  const counts = report?.mapping_counts;
  const confirmed = counts?.catalog_confirmed_products;
  const total = counts?.catalog_products;
  const valid =
    typeof confirmed === "number" &&
    typeof total === "number" &&
    confirmed <= total;
  const automatic =
    config?.enabled &&
    config.test_enabled &&
    config.targets.some((t) => t.automatic);
  return (
    <div className="products-summary" aria-label="Состояние товаров">
      <span>
        <i className="products-dot success" />
        <b>{valid ? confirmed : "—"}</b> сопоставлено в Ozon
      </span>
      <span>
        <i className="products-dot warning" />
        <b>{valid ? total - confirmed : "—"}</b> без подтверждённой пары
      </span>
      <span>
        <Icon name="catalog" size="button" />
        {!config
          ? "Настройки передачи загружаются"
          : automatic
            ? "Автопередача для выбранных товаров"
            : !config.enabled || !config.test_enabled
              ? "Передача выключена"
              : "Передача по подтверждению"}
      </span>
    </div>
  );
}

function ImportMenu() {
  const [open, setOpen] = useState(false);
  const trigger = useRef<HTMLButtonElement>(null);
  return (
    <div
      className="products-import-menu"
      onBlur={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget)) setOpen(false);
      }}
      onKeyDown={(event) => {
        if (event.key === "Escape") {
          setOpen(false);
          trigger.current?.focus();
        }
      }}
    >
      <button
        ref={trigger}
        className="primary-button"
        aria-expanded={open}
        aria-controls="products-import-directions"
        onClick={() => setOpen(!open)}
      >
        Импорт товаров <Icon name="chevron-down" size="button" />
      </button>
      {open && (
        <nav id="products-import-directions" aria-label="Направление импорта">
          <Link
            to="/products/import/ozon-to-moysklad"
            onClick={() => setOpen(false)}
          >
            <BrandLogo provider="ozon" decorative />
            Ozon → МойСклад
          </Link>
          <Link
            to="/products/import/moysklad-to-ozon"
            onClick={() => setOpen(false)}
          >
            <BrandLogo provider="moysklad" decorative />
            МойСклад → Ozon
          </Link>
          <Link to="/products/import" onClick={() => setOpen(false)}>
            Об импорте товаров
          </Link>
        </nav>
      )}
    </div>
  );
}

function ProductImportHub() {
  return (
    <>
      <div className="products-import-hub">
        {(["ozon", "moysklad"] as const).map((provider) => (
          <article key={provider}>
            <div className="products-import-logos">
              <BrandLogo provider={provider} decorative />
              <span>→</span>
              <BrandLogo
                provider={provider === "ozon" ? "moysklad" : "ozon"}
                decorative
              />
            </div>
            <h2>
              {provider === "ozon"
                ? "Из Ozon в МойСклад"
                : "Из МойСклад в Ozon"}
            </h2>
            <p>
              {provider === "ozon"
                ? "Создайте отсутствующие товары в МойСклад. Проверим существующие связи и результат предыдущих отправок."
                : "Подготовьте карточки Ozon: категорию, цену, НДС, габариты и обязательные характеристики."}
            </p>
            <Link
              className="primary-button"
              to={`/products/import/${provider === "ozon" ? "ozon-to-moysklad" : "moysklad-to-ozon"}`}
            >
              {provider === "ozon" ? "Выбрать товары" : "Проверить товары"}
            </Link>
          </article>
        ))}
      </div>
      <ol className="products-steps">
        <li>Выберите направление</li>
        <li>Проверьте товары</li>
        <li>Подтвердите создание</li>
      </ol>
      <p className="products-notice">
        <Icon name="diagnostics" size="button" />
        Связанные товары повторно не создаются. Отправка начинается после
        подтверждения.<Link to="/products/history">История отправок</Link>
      </p>
    </>
  );
}
