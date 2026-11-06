import { useState } from "react";
import { Link, useLocation, useSearchParams } from "react-router-dom";
import type { Account } from "./api";
import type { Confirmation, Scope } from "./Confirmation";
import { BrandLogo } from "./BrandLogo";
import { Icon, PageHeader, StatePanel } from "./UI";
import {
  useWarehouseOverview,
  warehouseHref,
  type WarehouseRow,
} from "./WarehouseData";
import {
  WarehouseEmpty,
  WarehouseFailure,
  WarehouseInfo,
  WarehousePagination,
  WarehouseStatus,
  WarehouseType,
  StockAge,
} from "./WarehouseParts";
import { WarehouseMappingDrawer } from "./WarehouseMappingDrawer";
import "./warehouses.css";

const tabs = [
  ["", "Все"],
  ["mapped", "Сопоставлены"],
  ["unmapped", "Без связи"],
  ["attention", "Требуют внимания"],
] as const;
export function WarehouseOverview({
  scope,
  confirmation,
  accounts = [],
}: {
  scope: Scope;
  confirmation: Confirmation;
  accounts?: Account[];
}) {
  const [query, setQuery] = useSearchParams();
  const location = useLocation();
  const [selected, setSelected] = useState<WarehouseRow | null>(null);
  const filter = tabs.some(([value]) => value === query.get("filter"))
    ? query.get("filter") || ""
    : "";
  const search = (query.get("search") || "").slice(0, 256);
  const offset = Math.max(
    0,
    Math.min(100000, Number(query.get("offset")) || 0),
  );
  const data = useWarehouseOverview(scope, { search, filter, offset });
  function update(key: string, value: string) {
    setQuery(
      (previous) => {
        const next = new URLSearchParams(previous);
        next.delete("offset");
        if (value) next.set(key, value);
        else next.delete(key);
        return next;
      },
      { replace: key === "search" },
    );
  }
  const counts = data.data?.counts;
  const count = (key: string) =>
    counts
      ? key === ""
        ? counts.total
        : counts[key as "mapped" | "unmapped" | "attention"]
      : "—";
  const accountName = (source: string, id: string) => {
    const providers = accounts.filter((account) => account.source === source);
    const index = providers.findIndex((account) => account.account === id);
    return index >= 0
      ? providers[index].name || `Аккаунт ${index + 1}`
      : "Аккаунт не выбран";
  };
  return (
    <div className="wh-page">
      <nav className="wh-breadcrumb" aria-label="Хлебные крошки">
        Склады / Управление складами
      </nav>
      <PageHeader
        title="Склады"
        description="Сопоставьте склады Ozon и МойСклад и проверяйте остатки без путаницы"
      />
      <div className="wh-accounts">
        <BrandLogo provider="ozon" size="sm" decorative />
        <span>
          Ozon: <strong>{accountName("ozon", scope.ozon)}</strong>
        </span>
        <span aria-hidden="true">↔</span>
        <BrandLogo provider="moysklad" size="sm" decorative />
        <span>
          МойСклад: <strong>{accountName("moysklad", scope.moy)}</strong>
        </span>
      </div>
      <div className="wh-metrics">
        {(
          [
            ["mapped", "Сопоставлены", "check"],
            ["unmapped", "Без сопоставления", "integrations"],
            ["attention", "Требуют внимания", "error"],
          ] as const
        ).map(([key, label, icon]) => (
          <button
            key={key}
            className="wh-metric"
            data-state={key}
            aria-pressed={filter === key}
            onClick={() => update("filter", filter === key ? "" : key)}
          >
            <span className="wh-metric-icon">
              <Icon name={icon} size="metric" />
            </span>
            <span>
              <b>{data.isError ? "—" : count(key)}</b>
              <span>{label}</span>
            </span>
          </button>
        ))}
      </div>
      <section className="wh-surface" aria-label="Список складов">
        <div className="wh-toolbar">
          <label className="wh-search">
            <Icon name="search" />
            <input
              aria-label="Поиск склада"
              placeholder="Поиск по названию склада"
              value={search}
              maxLength={256}
              onChange={(e) => update("search", e.target.value)}
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
        <nav className="wh-tabs" aria-label="Состояние сопоставления">
          {tabs.map(([key, label]) => (
            <button
              key={key}
              className="wh-tab"
              aria-current={filter === key ? "page" : undefined}
              onClick={() => update("filter", key)}
            >
              {label} <span>({data.isError ? "—" : count(key)})</span>
            </button>
          ))}
        </nav>
        {!scope.moy || !scope.ozon ? (
          <WarehouseEmpty />
        ) : data.isLoading ? (
          <StatePanel kind="loading">Загружаем склады…</StatePanel>
        ) : data.error ? (
          <WarehouseFailure
            error={data.error}
            retry={() => void data.refetch()}
          />
        ) : !data.data?.items.length ? (
          <WarehouseEmpty
            search={!!search || !!filter}
            imported={data.data?.ozon_imported}
          />
        ) : (
          <div className="wh-table-wrap">
            <table className="wh-table wh-overview-table">
              <thead>
                <tr>
                  <th>Склад Ozon</th>
                  <th>Тип</th>
                  <th>Склад МойСклад</th>
                  <th>Данные об остатках</th>
                  <th>Состояние</th>
                  <th>Действие</th>
                </tr>
              </thead>
              <tbody>
                {data.data.items.map((row) => (
                  <tr key={row.id}>
                    <td data-label="Склад Ozon">
                      <div className="wh-name">
                        <span className="wh-warehouse-icon">
                          <Icon name="warehouse" />
                        </span>
                        <span>
                          <Link to={warehouseHref(row.id, location.search)}>
                            {row.name || "Название недоступно"}
                          </Link>
                          <small>Ozon</small>
                        </span>
                      </div>
                    </td>
                    <td data-label="Тип">
                      <WarehouseType row={row} />
                    </td>
                    <td data-label="МойСклад">
                      <span className="wh-store">
                        {row.store_id && (
                          <Icon name="warehouse" size="button" />
                        )}
                        <span>
                          {row.store_id
                            ? row.store_name || "Название недоступно"
                            : "Не выбран"}
                        </span>
                      </span>
                    </td>
                    <td data-label="Остатки">
                      <StockAge date={row.observed_at} rows={row.stock_rows} />
                    </td>
                    <td data-label="Состояние">
                      <WarehouseStatus state={row.state} />
                    </td>
                    <td data-label="Действие">
                      {row.state === "mapped" ? (
                        <Link
                          className="wh-open"
                          to={warehouseHref(row.id, location.search)}
                        >
                          Открыть <Icon name="arrow" size="button" />
                        </Link>
                      ) : (
                        <button
                          className="secondary wh-map"
                          onClick={() => setSelected(row)}
                        >
                          {row.state === "unmapped"
                            ? "Сопоставить"
                            : "Проверить"}
                        </button>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        {data.data && (
          <WarehousePagination
            offset={offset}
            size={25}
            count={data.data.items.length}
            more={data.data.has_next}
            busy={data.isFetching}
            change={(value) => update("offset", String(value))}
          />
        )}
        <WarehouseInfo>
          Сопоставление складов само по себе не включает синхронизацию остатков.
        </WarehouseInfo>
      </section>
      {selected && (
        <WarehouseMappingDrawer
          scope={scope}
          row={selected}
          confirmation={confirmation}
          close={() => setSelected(null)}
        />
      )}
    </div>
  );
}
