import { Link, NavLink, useLocation, useNavigate } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { api, params, type Report } from "./api";
import type { Confirmation, Scope } from "./Confirmation";
import { ProductMappingPage } from "./ProductMappingPage";
import { ProductAutoMatching } from "./ProductAutoMatching";
import { ProductImportMenu } from "./ProductImportMenu";
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
import "./products-reference.css";

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
  const { pathname, state, search } = useLocation();
  const navigate = useNavigate();
  const operationID = new URLSearchParams(search).get("operation") || undefined;
  const requested = state as {
    scope?: Scope;
    products?: { id: string; offer: string }[];
  } | null;
  const initialProducts =
    requested?.scope?.tenant === scope.tenant &&
    requested.scope.moy === scope.moy &&
    requested.scope.ozon === scope.ozon
      ? requested.products
      : undefined;
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
      "rules/automation",
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
  const automationPage = route === "rules/automation";
  const primaryPage = ["", "rules", "history"].includes(route);
  const title = automationPage
    ? "Автоматическая передача"
    : route === "transfer"
      ? operationID
        ? "Передача изменений"
        : "Проверьте изменения перед отправкой"
      : matchingPage
        ? "Автосопоставление товаров"
        : route === "import"
          ? "Импорт товаров"
          : route === "import/ozon-to-moysklad"
            ? "Выберите товары для создания в МойСклад"
            : route === "import/moysklad-to-ozon"
              ? "Проверьте товары перед созданием в Ozon"
              : "Товары";
  return (
    <div className="products-workspace">
      {!primaryPage && (
        <nav className="products-breadcrumb" aria-label="Путь к разделу">
          <Link to="/products">Товары</Link>
          <span>/</span>
          <span>
            {automationPage
              ? "Правила синхронизации / Автоматизация"
              : matchingPage
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
          {automationPage
            ? "Разрешите обновлять конкретные поля у выбранных связанных товаров"
            : matchingPage
              ? "STYK найдёт совпадения, а неоднозначные варианты предложит проверить."
              : route === "rules"
                ? "Правила синхронизации"
                : route === "transfer"
                  ? operationID
                    ? "Запросы поставлены в обработку. Проверим результат в каждом сервисе."
                    : "До подтверждения товары не изменятся ни в Ozon, ни в МойСклад"
                  : importPage
                    ? "Переносите товары между сервисами. Перед созданием проверим соответствия и обязательные поля."
                    : "Управляйте соответствиями и изменениями товаров в одном месте"}
        </p>
      </header>
      <div className="products-account-pair">
        <span title={moyLabel ? `МойСклад: ${moyLabel}` : "МойСклад"}>
          <BrandLogo provider="moysklad" decorative />
          МойСклад
          {moyLabel && (
            <span className="products-account-name">
              : <b>{moyLabel}</b>
            </span>
          )}
        </span>
        <span aria-hidden="true">↔</span>
        <span title={ozonLabel ? `Ozon: ${ozonLabel}` : "Ozon"}>
          <BrandLogo provider="ozon" decorative />
          Ozon
          {ozonLabel && (
            <span className="products-account-name">
              : <b>{ozonLabel}</b>
            </span>
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
          {primaryPage && route !== "rules" && (
            <ProductsSummary report={report.data} config={config.data} />
          )}
          {primaryPage && (
            <div className="products-toolbar">
              <nav className="products-tabs" aria-label="Товары">
                <NavLink end to="/products">
                  Все товары
                </NavLink>
                <NavLink to="/products/rules">Правила синхронизации</NavLink>
                <NavLink to="/products/history">История отправок</NavLink>
              </nav>
              {route === "" && (
                <div className="products-actions">
                  <Link className="secondary" to="/products/matching">
                    <Icon name="search" size="button" />
                    Автосопоставление
                  </Link>
                  <ProductImportMenu />
                  <Link
                    className="secondary products-settings-link"
                    aria-label="Настройки передачи"
                    to="/products/rules/automation"
                  >
                    <Icon name="settings" size="button" />
                  </Link>
                </div>
              )}
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
          {automationPage && <CatalogSync scope={scope} view="automation" />}
          {route === "history" && (
            <div className="catalog-sync">
              <CatalogSyncOperations scope={scope} />
            </div>
          )}
          {route === "transfer" && (
            <CatalogSync
              scope={scope}
              view="transfer"
              operationID={operationID}
              onApproved={(id) =>
                navigate(
                  `/products/transfer?operation=${encodeURIComponent(id)}`,
                )
              }
              initialProducts={initialProducts}
            />
          )}
          {route === "import" && <ProductImportHub scope={scope} />}
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
        <span className="products-summary-value">
          <b>{valid ? confirmed : "—"}</b>
          <span>связано в Ozon</span>
        </span>
      </span>
      <span>
        <i className="products-dot warning" />
        <span className="products-summary-value">
          <b>{valid ? total - confirmed : "—"}</b>
          <span>в Ozon без пары</span>
        </span>
      </span>
      <span className="products-summary-mode">
        <Icon name="catalog" size="button" />
        <span>
          {!config
            ? "Настройки передачи загружаются"
            : automatic
              ? "Автопередача включена"
              : !config.enabled || !config.test_enabled
                ? "Передача выключена"
                : "Передача по подтверждению"}
        </span>
      </span>
    </div>
  );
}

function ProductImportHub({ scope }: { scope: Scope }) {
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
            <ImportDirectionSummary scope={scope} provider={provider} />
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

function ImportDirectionSummary({
  scope,
  provider,
}: {
  scope: Scope;
  provider: "ozon" | "moysklad";
}) {
  const query = useQuery({
    queryKey: ["import-summary", scope, provider],
    queryFn: ({ signal }) =>
      api<import("./CatalogSyncTypes").ProductPage>(
        catalogURL("products", scope, { provider, limit: "1" }),
        signal,
      ),
    retry: false,
  });
  const summary = query.data?.summary;
  return (
    <div className={`products-hub-summary ${provider}`} aria-live="polite">
      {summary ? (
        <>
          <b>{summary.linked}</b> уже связаны <span>·</span>
          <b>{summary.to_review}</b> к проверке
        </>
      ) : query.isError ? (
        "Сводка недоступна — откройте список товаров"
      ) : (
        "Загружаем состояние каталога…"
      )}
    </div>
  );
}
