import { useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "./api";
import { Link } from "react-router-dom";
import { BrandLogo } from "./BrandLogo";
import {
  catalogURL,
  productName,
  type ProductPage,
  type Scope,
} from "./CatalogSyncTypes";
import {
  fieldLabel,
  type Authority,
  type CatalogConfig,
  type Enrollment,
} from "./CatalogSyncTypes";

export function needsAutomaticConsent(
  config: CatalogConfig,
  targets: Enrollment[],
  policies: Record<string, Authority>,
  enabled: boolean,
) {
  return targets.some((target) => {
    if (!target.automatic) return false;
    const previous = config.targets.find((item) => item.id === target.id);
    return (
      (!config.enabled && enabled) ||
      !previous?.automatic ||
      (target.automatic_fields || []).some(
        (field) =>
          !previous.automatic_fields?.includes(field) ||
          config.rules[field] !== policies[field],
      )
    );
  });
}

export function CatalogSyncAutomation({
  scope,
  config,
  targets,
  policies,
  enabled,
  busy,
  actions,
  onChange,
}: {
  scope: Scope;
  config: CatalogConfig;
  targets: Enrollment[];
  policies: Record<string, Authority>;
  enabled: boolean;
  busy: boolean;
  actions: ReactNode;
  onChange: (targets: Enrollment[]) => void;
}) {
  const [selected, setSelected] = useState<string[]>([]);
  const [filter, setFilter] = useState("");
  const [fields, setFields] = useState<string[]>([]);
  const [cursor, setCursor] = useState("");
  const [pages, setPages] = useState<string[]>([]);
  const query = useQuery({
    queryKey: ["catalog-automation-products", scope, cursor],
    queryFn: ({ signal }) =>
      api<ProductPage>(
        catalogURL("products", scope, {
          provider: "moysklad",
          limit: "20",
          cursor,
        }),
        signal,
      ),
    retry: false,
  });
  const allowed = ["name", "description"].filter(
    (f) =>
      (!config.fields ||
        config.fields.some((c) => c.field === f && c.writable)) &&
      ["MOYSKLAD_AUTHORITATIVE", "OZON_AUTHORITATIVE"].includes(policies[f]),
  );
  const available = enabled && config.publication_available === true;
  const current = new Map(targets.map((t) => [t.id, t]));
  const options = [
    ...targets,
    ...(query.data?.enrollments || []).filter(
      (t) => !targets.some((existing) => existing.mapping_id === t.mapping_id),
    ),
  ].filter((t) => t.mapping_id && t.target.id && !t.create);
  const unpaired = (query.data?.products || []).filter(
    (product) =>
      !options.some((target) =>
        [target.source, target.target].some(
          (identity) =>
            identity.account.provider === product.identity.account.provider &&
            identity.id === product.identity.id,
        ),
      ),
  );
  function update(items: Enrollment[]) {
    const patches = new Map(items.map((t) => [t.id, t]));
    onChange([
      ...targets.map((t) => patches.get(t.id) || t),
      ...items.filter((t) => !current.has(t.id)),
    ]);
  }
  function patch(t: Enrollment, value: Partial<Enrollment>) {
    update([{ ...t, ...value }]);
  }
  const displayed = options.filter(
    (t) => !filter || (filter === "enabled" ? t.automatic : !t.automatic),
  );
  return (
    <>
      <h2 id="catalog-config-title" className="products-sr">
        Автоматическая передача
      </h2>
      <p className="products-notice">
        Без подтверждения передаются только разрешённые поля. Создание новых
        товаров и конфликты проверяются вручную.
      </p>
      {!available && (
        <p className="catalog-warning">
          Включите синхронизацию в правилах и проверьте разрешения аккаунтов.
          Действующие разрешения можно отключить.
        </p>
      )}
      <div className="products-filter-pills">
        {[
          ["", "Все"],
          ["enabled", "Разрешено"],
          ["attention", "Без автопередачи"],
        ].map(([value, label]) => (
          <button
            type="button"
            key={value}
            className={filter === value ? "active" : "secondary"}
            aria-pressed={filter === value}
            onClick={() => setFilter(value)}
          >
            {label}{" "}
            <b>
              {value === ""
                ? options.length + unpaired.length
                : value === "enabled"
                  ? options.filter((t) => t.automatic).length
                  : options.filter((t) => !t.automatic).length +
                    unpaired.length}
            </b>
          </button>
        ))}
      </div>
      <div className="products-automation-layout">
        <div className="products-rules-table">
          <div className="products-automation-table-scroll">
            <table>
              <thead>
                <tr>
                  <th>
                    <input
                      type="checkbox"
                      aria-label="Выбрать все доступные разрешения"
                      checked={
                        displayed.length > 0 &&
                        displayed.every((target) =>
                          selected.includes(target.id),
                        )
                      }
                      disabled={busy || !displayed.length}
                      onChange={(event) =>
                        setSelected(
                          event.target.checked
                            ? [
                                ...new Set([
                                  ...selected,
                                  ...displayed.map((target) => target.id),
                                ]),
                              ]
                            : selected.filter(
                                (id) =>
                                  !displayed.some((target) => target.id === id),
                              ),
                        )
                      }
                    />
                  </th>
                  <th>Товар и связь</th>
                  <th>Название</th>
                  <th>Описание</th>
                  <th>Автопередача</th>
                  <th>Состояние</th>
                </tr>
              </thead>
              <tbody>
                {displayed.map((t) => {
                  const product = query.data?.products.find((p) =>
                    [t.source, t.target].some(
                      (i) =>
                        i.account.provider === p.identity.account.provider &&
                        i.id === p.identity.id,
                    ),
                  );
                  return (
                    <tr
                      key={t.id}
                      className={selected.includes(t.id) ? "is-selected" : ""}
                    >
                      <td>
                        <input
                          type="checkbox"
                          aria-label={`Выбрать разрешения ${t.source.offer_id || t.source.id}`}
                          checked={selected.includes(t.id)}
                          disabled={busy}
                          onChange={(e) =>
                            setSelected(
                              e.target.checked
                                ? [...selected, t.id]
                                : selected.filter((id) => id !== t.id),
                            )
                          }
                        />
                      </td>
                      <td>
                        <div className="products-identity">
                          <span className="products-image-placeholder">
                            <BrandLogo provider="moysklad" decorative />
                          </span>
                          <div>
                            <b>
                              {product
                                ? productName(product)
                                : t.source.offer_id || t.source.id}
                            </b>
                            <small className="mapping-chip ok">Связан</small>
                          </div>
                        </div>
                      </td>
                      {["name", "description"].map((f) => (
                        <td key={f}>
                          <label className="products-field-permission">
                            <input
                              type="checkbox"
                              aria-label={`${fieldLabel(f)}: ${t.source.offer_id || t.source.id}`}
                              checked={t.automatic_fields?.includes(f) || false}
                              disabled={
                                busy ||
                                (!t.automatic_fields?.includes(f) &&
                                  (!available || !allowed.includes(f)))
                              }
                              onChange={(e) =>
                                patch(t, {
                                  automatic_fields: e.target.checked
                                    ? [...(t.automatic_fields || []), f]
                                    : (t.automatic_fields || []).filter(
                                        (v) => v !== f,
                                      ),
                                })
                              }
                            />
                            <span>
                              {t.automatic_fields?.includes(f)
                                ? "Разрешено"
                                : "Вручную"}
                            </span>
                          </label>
                        </td>
                      ))}
                      <td>
                        <label className="products-switch">
                          <input
                            type="checkbox"
                            aria-label={`Автоматически передавать изменения товара ${t.source.offer_id || t.source.id}`}
                            checked={t.automatic}
                            disabled={
                              busy ||
                              (!t.automatic && (!available || !allowed.length))
                            }
                            onChange={(e) =>
                              patch(t, { automatic: e.target.checked })
                            }
                          />
                          <span aria-hidden="true" />
                        </label>
                      </td>
                      <td>
                        <span className="mapping-chip ok">Связан</span>
                      </td>
                    </tr>
                  );
                })}
                {filter !== "enabled" &&
                  unpaired.map((product) => (
                    <tr key={`unpaired-${product.identity.id}`}>
                      <td>
                        <input
                          type="checkbox"
                          disabled
                          aria-label={`Нет пары: ${productName(product)}`}
                        />
                      </td>
                      <td>
                        <div className="products-identity">
                          <span className="products-image-placeholder">
                            <BrandLogo provider="moysklad" decorative />
                          </span>
                          <div>
                            <b>{productName(product)}</b>
                            <small className="mapping-chip">Нет пары</small>
                          </div>
                        </div>
                      </td>
                      <td>—</td>
                      <td>—</td>
                      <td>
                        <label className="products-switch">
                          <input
                            type="checkbox"
                            disabled
                            aria-label={`Автопередача недоступна: ${productName(product)}`}
                          />
                          <span aria-hidden="true" />
                        </label>
                      </td>
                      <td>
                        <span className="products-muted">Нет пары</span>
                      </td>
                    </tr>
                  ))}
              </tbody>
            </table>
          </div>
          {query.isLoading && <p role="status">Загружаем связанные товары…</p>}
          {query.isError && (
            <p role="alert">
              Не удалось загрузить товары.{" "}
              <button type="button" onClick={() => void query.refetch()}>
                Повторить
              </button>
            </p>
          )}
          {!query.isLoading && !options.length && !unpaired.length && (
            <div className="catalog-empty">
              Пока нет связанных товаров.{" "}
              <Link to="/products">Сопоставить товары</Link>
            </div>
          )}
          <div className="catalog-actions products-automation-pagination">
            <button
              type="button"
              className="secondary"
              disabled={!pages.length || query.isFetching}
              onClick={() => {
                setCursor(pages.at(-1) || "");
                setPages(pages.slice(0, -1));
              }}
            >
              Назад
            </button>
            <span>Страница {pages.length + 1}</span>
            <button
              type="button"
              className="secondary"
              disabled={!query.data?.next_cursor || query.isFetching}
              onClick={() => {
                setPages([...pages, cursor]);
                setCursor(query.data!.next_cursor!);
              }}
            >
              Далее
            </button>
          </div>
        </div>
        <aside className="products-automation-bulk">
          <h3>
            Для выбранных товаров <span>{selected.length}</span>
          </h3>
          {allowed.map((f) => (
            <label className="catalog-check" key={f}>
              <input
                type="checkbox"
                checked={fields.includes(f)}
                disabled={busy || !available}
                onChange={(e) =>
                  setFields(
                    e.target.checked
                      ? [...fields, f]
                      : fields.filter((v) => v !== f),
                  )
                }
              />
              {fieldLabel(f)}
            </label>
          ))}
          <p>
            Примените поля к выбранным товарам, затем подтвердите и сохраните
            разрешения.
          </p>
          <button
            type="button"
            disabled={busy || !available || !selected.length || !fields.length}
            onClick={() =>
              update(
                options
                  .filter((t) => selected.includes(t.id))
                  .map((t) => ({
                    ...t,
                    automatic: true,
                    automatic_fields: fields,
                  })),
              )
            }
          >
            Применить к выбранным
          </button>
          <button
            type="button"
            className="secondary"
            disabled={busy || !selected.length}
            onClick={() =>
              update(
                options
                  .filter((t) => selected.includes(t.id))
                  .map((t) => ({ ...t, automatic: false })),
              )
            }
          >
            Отключить автопередачу
          </button>
          {actions}
        </aside>
      </div>
      <Link className="products-back-link" to="/products/rules">
        ← Вернуться к правилам
      </Link>
    </>
  );
}
