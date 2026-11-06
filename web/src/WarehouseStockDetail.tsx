import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link, useLocation, useParams } from "react-router-dom";
import { api, params, type Page, type Stock } from "./api";
import type { Confirmation, Scope } from "./Confirmation";
import { Icon, PageHeader, StatePanel } from "./UI";
import { useWarehouseOverview } from "./WarehouseData";
import {
  StockAge,
  WarehouseFailure,
  WarehouseInfo,
  WarehousePair,
  WarehousePagination,
  WarehouseStatus,
  WarehouseType,
} from "./WarehouseParts";
import { WarehouseMappingDrawer } from "./WarehouseMappingDrawer";
import "./warehouses.css";

type NamedStock = Stock & { name?: string; article?: string };
export function WarehouseStockDetail({
  scope,
  confirmation,
}: {
  scope: Scope;
  confirmation: Confirmation;
}) {
  const { warehouseId = "" } = useParams();
  const location = useLocation();
  const overview = useWarehouseOverview(scope, { warehouse: warehouseId });
  const [mapping, setMapping] = useState(false);
  const [source, setSource] = useState<"ozon" | "moysklad">("ozon");
  const row = overview.data?.items[0];
  const back = "/warehouses" + location.search;
  return (
    <div className="wh-page">
      <nav className="wh-breadcrumb" aria-label="Хлебные крошки">
        <Link to={back}>Склады</Link> / {row?.name || "Детали склада"}
      </nav>
      <Link className="wh-back" to={back}>
        <Icon name="chevron-left" size="button" />
        Ко всем складам
      </Link>
      {overview.isLoading ? (
        <StatePanel kind="loading">Загружаем склад…</StatePanel>
      ) : overview.error ? (
        <WarehouseFailure
          error={overview.error}
          retry={() => void overview.refetch()}
        />
      ) : !row ? (
        <StatePanel>
          <div>
            <h1>Склад недоступен</h1>
            <p>
              Его нет в данных выбранных аккаунтов. Проверьте аккаунты или
              вернитесь к списку складов.
            </p>
            <Link to={back}>Ко всем складам</Link>
          </div>
        </StatePanel>
      ) : (
        <>
          <PageHeader
            title={row.name || "Название склада недоступно"}
            description="Остатки и сведения о выбранном складе"
          >
            <div className="wh-heading-badges">
              <WarehouseType row={row} />
              <WarehouseStatus state={row.state} />
            </div>
          </PageHeader>
          {row.state === "mapped" ? (
            <div className="wh-surface wh-detail-pair">
              <WarehousePair ozonName={row.name} storeName={row.store_name} />
              <span className="wh-confirmed">
                <Icon name="check" size="button" />
                Соответствие подтверждено
              </span>
            </div>
          ) : (
            <div className="wh-surface wh-unmapped">
              <div>
                <strong>
                  Для этого склада нет доступного подтверждённого соответствия
                </strong>
                <p>
                  Данные МойСклад появятся после успешной проверки и
                  сопоставления.
                </p>
              </div>
              <button
                className="mapping-primary"
                onClick={() => setMapping(true)}
              >
                Проверить сопоставление
              </button>
            </div>
          )}
          <section
            className="wh-surface wh-stock"
            aria-label="Остатки на складе"
          >
            <h2>Остатки на складе</h2>
            <p className="wh-subtitle">
              Значения по последним полученным данным
            </p>
            <div
              className="wh-source-tabs"
              role="group"
              aria-label="Источник остатков"
            >
              <button
                className={source === "ozon" ? "active" : "secondary"}
                aria-pressed={source === "ozon"}
                onClick={() => setSource("ozon")}
              >
                Ozon
              </button>
              <button
                className={source === "moysklad" ? "active" : "secondary"}
                aria-pressed={source === "moysklad"}
                onClick={() => setSource("moysklad")}
              >
                МойСклад
              </button>
            </div>
            {source === "moysklad" && row.state !== "mapped" ? (
              <WarehouseInfo>
                Остатки МойСклад недоступны: сначала подтвердите соответствие
                складов.
              </WarehouseInfo>
            ) : (
              <WarehouseStockTable
                key={`${source}:${warehouseId}:${row.store_id}`}
                scope={scope}
                source={source}
                warehouse={source === "ozon" ? warehouseId : row.store_id}
              />
            )}
            <details className="wh-technical">
              <summary>Технические сведения</summary>
              <dl>
                <dt>Склад Ozon</dt>
                <dd>{row.id}</dd>
                <dt>Тип в Ozon</dt>
                <dd>{row.warehouse_type || "Неизвестен"}</dd>
                <dt>Склад МойСклад</dt>
                <dd>{row.store_id || "Не выбран"}</dd>
                <dt>Соответствие</dt>
                <dd>{row.mapping_id ?? "Не подтверждено"}</dd>
                <dt>Подтверждено</dt>
                <dd>
                  {row.confirmed_at
                    ? new Date(row.confirmed_at).toLocaleString("ru-RU")
                    : "—"}
                </dd>
              </dl>
            </details>
          </section>
          {mapping && (
            <WarehouseMappingDrawer
              scope={scope}
              row={row}
              confirmation={confirmation}
              close={() => setMapping(false)}
            />
          )}
        </>
      )}
    </div>
  );
}
export function WarehouseStockTable({
  scope,
  source,
  warehouse,
}: {
  scope: Scope;
  source: "ozon" | "moysklad";
  warehouse: string;
}) {
  const [search, setSearch] = useState("");
  const [offset, setOffset] = useState(0);
  const account = source === "ozon" ? scope.ozon : scope.moy;
  const data = useQuery({
    queryKey: ["stock", scope, source, account, warehouse, search, offset],
    queryFn: ({ signal }) =>
      api<Page<NamedStock>>(
        "/api/stock?" +
          params({
            tenant: scope.tenant,
            source,
            account,
            warehouse,
            search,
            offset,
          }),
        signal,
      ),
    retry: false,
  });
  const columns =
    source === "ozon"
      ? ([
          ["present", "На складе"],
          ["reserved", "В резерве"],
          ["free_stock", "Свободно"],
        ] as const)
      : ([
          ["stock", "На складе"],
          ["reserve", "В резерве"],
          ["incoming", "В пути"],
          ["quantity", "Доступно"],
        ] as const);
  return (
    <div className="wh-stock-table">
      <div className="wh-toolbar">
        <label className="wh-search">
          <Icon name="search" />
          <input
            value={search}
            maxLength={256}
            aria-label="Найти товар или артикул"
            placeholder="Найти товар или артикул"
            onChange={(e) => {
              setSearch(e.target.value);
              setOffset(0);
            }}
          />
        </label>
        <button
          className="secondary"
          disabled={data.isFetching}
          onClick={() => void data.refetch()}
        >
          <Icon name="refresh" size="button" />
          Обновить список
        </button>
      </div>
      {data.isLoading ? (
        <StatePanel kind="loading">Загружаем остатки…</StatePanel>
      ) : data.error ? (
        <WarehouseFailure
          error={data.error}
          retry={() => void data.refetch()}
        />
      ) : !data.data?.items.length ? (
        <StatePanel>
          <div>
            <h3>{search ? "Товары не найдены" : "Нет данных об остатках"}</h3>
            <p>
              {search
                ? "Измените название или артикул в поиске."
                : "Количество неизвестно. Отсутствие записей не означает нулевой остаток."}
            </p>
          </div>
        </StatePanel>
      ) : (
        <div className="wh-table-wrap">
          <table className="wh-table wh-stock-values">
            <thead>
              <tr>
                <th>Товар / артикул</th>
                {columns.map(([key, label]) => (
                  <th key={key}>{label}</th>
                ))}
                <th>Данные</th>
              </tr>
            </thead>
            <tbody>
              {data.data.items.map((item) => (
                <tr key={`${item.kind}:${item.item_id}`}>
                  <td data-label="Товар">
                    <div className="wh-name">
                      <span className="wh-product-icon">
                        <Icon
                          name={
                            item.kind === "variant" ? "variants" : "products"
                          }
                        />
                      </span>
                      <span>
                        <strong>
                          {item.name ||
                            `${item.kind === "variant" ? "Вариант" : "Товар"} ${item.item_id}`}
                        </strong>
                        <small>{item.article || "Артикул недоступен"}</small>
                      </span>
                    </div>
                  </td>
                  {columns.map(([key, label]) => (
                    <td className="wh-number" key={key} data-label={label}>
                      {item[key] ?? "—"}
                    </td>
                  ))}
                  <td data-label="Данные">
                    <StockAge date={item.observed_at} />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <WarehouseInfo>
        «0» — подтверждённый нулевой остаток. «—» — данные отсутствуют или
        неизвестны. Обновление списка перечитывает сохранённые данные STYK.
      </WarehouseInfo>
      {source === "moysklad" && (
        <p className="wh-stock-note">
          Значения «Доступно» и «В пути» получены из МойСклад. Остатки Ozon
          показаны отдельно.
        </p>
      )}
      {data.data && (
        <WarehousePagination
          offset={offset}
          size={50}
          count={data.data.items.length}
          more={data.data.has_next}
          busy={data.isFetching}
          change={setOffset}
        />
      )}
    </div>
  );
}
