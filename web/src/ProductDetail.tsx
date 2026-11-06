import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { api, type Product } from "./api";
import { IntegrationDialog } from "./IntegrationDialog";
import { BrandLogo } from "./BrandLogo";
import { Icon } from "./UI";
import { CatalogSyncOperations } from "./CatalogSyncOperations";
import {
  catalogURL,
  displayValue,
  fieldLabel,
  type CatalogConfig,
  type ProductPage,
  type Scope,
} from "./CatalogSyncTypes";

export function ProductDetail({
  product,
  scope,
  config,
  onClose,
  onMatch,
}: {
  product: Product;
  scope: Scope;
  config?: CatalogConfig;
  onClose: () => void;
  onMatch: () => void;
}) {
  const [tab, setTab] = useState("overview");
  const query = useQuery({
    queryKey: [
      "product-detail",
      scope,
      product.provider,
      product.product_id,
      product.target_id,
    ],
    queryFn: async ({ signal }) => {
      const pages = await Promise.all(
        (["moysklad", "ozon"] as const).map((provider) =>
          api<ProductPage>(
            catalogURL("products", scope, {
              provider,
              search:
                (provider === "moysklad"
                  ? product.provider === "moysklad"
                    ? product.product_id
                    : product.target_id
                  : product.product_id) || "",
              limit: "100",
            }),
            signal,
          ),
        ),
      );
      return pages.map((page, index) =>
        page.products.find(
          (p) =>
            p.identity.id ===
            (index === 0
              ? product.provider === "moysklad"
                ? product.product_id
                : product.target_id
              : product.product_id),
        ),
      );
    },
    retry: false,
  });
  const [moy, ozon] = query.data || [];
  const target = config?.targets.find((t) =>
    [t.source, t.target].some(
      (i) =>
        i.account.provider === product.provider && i.id === product.product_id,
    ),
  );
  const linked = product.match_status === "CONFIRMED";
  const selection = [{ id: product.product_id, offer: product.offer_id || "" }];
  return (
    <IntegrationDialog
      title={product.name || product.offer_id || "Карточка товара"}
      className="products-detail-dialog catalog-sync"
      closeLabel="Закрыть карточку товара"
      onClose={onClose}
    >
      <div className="products-detail-identity">
        <span className="products-image-placeholder">
          <Icon name="products" />
        </span>
        <div>
          <p className="products-detail-name" aria-hidden="true">
            {product.name || product.offer_id || "Карточка товара"}
          </p>
          <p>Артикул: {product.offer_id || "Не указан"}</p>
          <span className={`mapping-chip ${linked ? "ok" : "warning"}`}>
            {linked
              ? "Связан"
              : product.match_status === "CONFLICT"
                ? "Конфликт"
                : "Нет пары"}
          </span>
          <div className="products-detail-pair">
            <BrandLogo provider="moysklad" decorative />
            МойСклад ↔ <BrandLogo provider="ozon" decorative />
            Ozon
          </div>
        </div>
      </div>
      <nav className="products-detail-tabs" aria-label="Карточка товара">
        {[
          ["overview", "Обзор"],
          ["mapping", "Сопоставление"],
          ["transfer", "Передача данных"],
          ["history", "История"],
        ].map(([value, label]) => (
          <button
            key={value}
            className={tab === value ? "active" : ""}
            aria-pressed={tab === value}
            onClick={() => setTab(value)}
          >
            {label}
          </button>
        ))}
      </nav>
      {(tab === "overview" || tab === "transfer") && (
        <>
          <h3>
            {tab === "overview"
              ? "Что отличается между сервисами"
              : "Правила передачи данных"}
          </h3>
          {query.isLoading && <p role="status">Загружаем данные товара…</p>}
          {query.isError && (
            <p role="alert" className="catalog-warning">
              Не удалось прочитать данные.{" "}
              <button
                className="text-action"
                onClick={() => void query.refetch()}
              >
                Повторить
              </button>
            </p>
          )}
          <div className="products-detail-diffs">
            {["name", "description"].map((field) => {
              const left = moy?.fields[field],
                right = ozon?.fields[field];
              const known = left?.state === "known" && right?.state === "known";
              const equal =
                known &&
                JSON.stringify(left.data) === JSON.stringify(right.data);
              return (
                <section key={field}>
                  <div>
                    <b>{fieldLabel(field)}</b>
                    <span
                      className={`mapping-chip ${equal ? "ok" : "warning"}`}
                    >
                      {equal
                        ? "Совпадает"
                        : known
                          ? "Различается"
                          : "Нет полных данных"}
                    </span>
                  </div>
                  <div>
                    {equal ? (
                      <strong className="products-detail-equal">
                        {displayValue(left)}
                      </strong>
                    ) : (
                      <>
                        <p>
                          <BrandLogo provider="moysklad" decorative />
                          <b>МойСклад</b>
                          {left
                            ? displayValue(left)
                            : "Нет данных в загруженном каталоге"}
                        </p>
                        <p>
                          <BrandLogo provider="ozon" decorative />
                          <b>Ozon</b>
                          {right
                            ? displayValue(right)
                            : "Нет данных в загруженном каталоге"}
                        </p>
                      </>
                    )}
                    <small>
                      {!config
                        ? "Правила передачи загружаются"
                        : config.rules[field] === "MOYSKLAD_AUTHORITATIVE"
                          ? "МойСклад → Ozon"
                          : config?.rules[field] === "OZON_AUTHORITATIVE"
                            ? "Ozon → МойСклад"
                            : config?.rules[field] === "MANUAL"
                              ? "Значение задаётся вручную"
                              : "Передача поля выключена"}
                    </small>
                  </div>
                </section>
              );
            })}
          </div>
          <section className="products-detail-card">
            <header>
              <h3>Автоматическая передача</h3>
              <Link to="/products/rules/automation">Настроить</Link>
            </header>
            {["name", "description"].map((f) => (
              <p
                className={`products-permission-row ${
                  target?.automatic && target.automatic_fields?.includes(f)
                    ? "allowed"
                    : "manual"
                }`}
                key={f}
              >
                <span aria-hidden="true">
                  {target?.automatic && target.automatic_fields?.includes(f)
                    ? "✓"
                    : "—"}
                </span>
                {fieldLabel(f)} —{" "}
                {target?.automatic && target.automatic_fields?.includes(f)
                  ? "разрешено автоматически"
                  : "после подтверждения"}
              </p>
            ))}
          </section>
        </>
      )}
      {(tab === "overview" || tab === "mapping") && (
        <section className="products-detail-card">
          <h3>
            Связь с товаром {product.provider === "ozon" ? "МойСклад" : "Ozon"}
          </h3>
          <div className="products-detail-mapping-state">
            <div>
              <p className={linked ? "catalog-success" : "catalog-warning"}>
                {linked
                  ? "Соответствие подтверждено"
                  : product.match_status === "CONFLICT"
                    ? "Существующая связь требует проверки"
                    : "Соответствие ещё не подтверждено"}
              </p>
              <p>
                {product.target_name ||
                  "Проверьте конкретную карточку во втором сервисе."}
              </p>
              {product.shared_inventory && (
                <p>
                  Общий товар МойСклад используется несколькими предложениями
                  Ozon.
                </p>
              )}
            </div>
            <button className="secondary" onClick={onMatch}>
              {linked ? "Проверить соответствие" : "Сопоставить товар"}
            </button>
          </div>
        </section>
      )}
      {tab === "history" && (
        <CatalogSyncOperations
          scope={scope}
          identity={{ provider: product.provider, id: product.product_id }}
        />
      )}
      <footer className="products-detail-footer">
        <span>
          <Icon name="diagnostics" size="button" />
          Ничего не отправляется до подтверждения
        </span>
        {linked && product.provider === "ozon" ? (
          <Link
            className="primary-button"
            to="/products/transfer"
            state={{ scope, products: selection }}
          >
            Проверить изменения →
          </Link>
        ) : (
          <button className="secondary" onClick={onMatch}>
            Проверить связь
          </button>
        )}
      </footer>
    </IntegrationDialog>
  );
}
