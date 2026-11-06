import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "./api";
import type { Confirmation, Scope } from "./Confirmation";
import { IntegrationDialog } from "./IntegrationDialog";
import { BrandLogo } from "./BrandLogo";
import { Icon, StatePanel } from "./UI";
import {
  warehouseURL,
  type Candidate,
  type CandidatePage,
  type WarehouseRow,
} from "./WarehouseData";
import {
  WarehouseEmpty,
  WarehouseFailure,
  WarehouseInfo,
  WarehousePagination,
  WarehouseType,
} from "./WarehouseParts";
import { WarehouseReview } from "./WarehouseReview";

export function WarehouseMappingDrawer({
  scope,
  row,
  confirmation,
  close,
}: {
  scope: Scope;
  row: WarehouseRow;
  confirmation: Confirmation;
  close: () => void;
}) {
  const [search, setSearch] = useState("");
  const [offset, setOffset] = useState(0);
  const [candidate, setCandidate] = useState<Candidate | null>(null);
  const [checking, setChecking] = useState(false);
  useEffect(() => {
    if (!checking)
      document
        .querySelector<HTMLInputElement>(
          '.wh-drawer input[aria-label="Найти склад МойСклад"]',
        )
        ?.focus();
  }, [checking]);
  const query = useQuery({
    queryKey: ["warehouse-candidates", scope, search, offset],
    queryFn: ({ signal }) =>
      api<CandidatePage>(
        warehouseURL(scope, { view: "candidates", search, offset }),
        signal,
      ),
    retry: false,
  });
  return (
    <IntegrationDialog
      title="Сопоставить склады"
      className="wh-drawer"
      onClose={close}
      closeLabel="Закрыть сопоставление складов"
      initialFocus='input[aria-label="Найти склад МойСклад"]'
    >
      {checking && candidate ? (
        <WarehouseReview
          key={`${row.id}:${candidate.id}`}
          scope={scope}
          moy={candidate.id}
          ozon={row.id}
          confirmation={confirmation}
          close={close}
          back={() => setChecking(false)}
          embedded
          prepare
        />
      ) : (
        <section className="wh-selection">
          <span className="wh-step">Шаг 1 из 2 · Сопоставление</span>
          <h2>Выберите склад МойСклад</h2>
          <p className="wh-subtitle">
            Для склада Ozon «{row.name || "Название недоступно"}»
          </p>
          <div className="wh-source">
            <BrandLogo provider="ozon" decorative />
            <div>
              <strong>{row.name || "Название недоступно"}</strong>
              <small>Исходный склад</small>
            </div>
            <WarehouseType row={row} />
            <button className="text-action" onClick={close}>
              Изменить
            </button>
          </div>
          <div className="wh-down" aria-hidden="true">
            ↓
          </div>
          <h3>Соответствующий склад в МойСклад</h3>
          <label className="wh-search">
            <Icon name="search" />
            <input
              autoComplete="off"
              aria-label="Найти склад МойСклад"
              placeholder="Найти склад МойСклад"
              value={search}
              maxLength={256}
              onChange={(e) => {
                setSearch(e.target.value);
                setOffset(0);
              }}
            />
          </label>
          {query.isLoading ? (
            <StatePanel kind="loading">Загружаем доступные склады…</StatePanel>
          ) : query.error ? (
            <WarehouseFailure
              error={query.error}
              retry={() => void query.refetch()}
            />
          ) : !query.data?.items.length ? (
            <WarehouseEmpty search={!!search} />
          ) : (
            <fieldset className="wh-candidates">
              <legend className="sr-only">Выбор склада МойСклад</legend>
              {query.data.items.map((item) => (
                <label
                  key={item.id}
                  className="wh-candidate"
                  data-selected={candidate?.id === item.id}
                  aria-disabled={!item.available}
                >
                  <input
                    type="radio"
                    name="warehouse-candidate"
                    value={item.id}
                    checked={candidate?.id === item.id}
                    disabled={!item.available}
                    onChange={() => setCandidate(item)}
                  />
                  <span className="wh-warehouse-icon">
                    <Icon name="warehouse" />
                  </span>
                  <span>
                    <strong>{item.name || "Название склада недоступно"}</strong>
                    <small>
                      {item.available
                        ? "Доступен для сопоставления"
                        : "Уже связан с другим складом"}
                    </small>
                  </span>
                </label>
              ))}
            </fieldset>
          )}
          {query.data && (query.data.has_next || offset > 0) && (
            <WarehousePagination
              offset={offset}
              size={25}
              count={query.data.items.length}
              more={query.data.has_next}
              busy={query.isFetching}
              change={setOffset}
            />
          )}
          {candidate &&
            !query.data?.items.some((item) => item.id === candidate.id) && (
              <p className="wh-selected-note">
                Выбран: {candidate.name || "Склад без названия"}
              </p>
            )}
          <WarehouseInfo>
            Проверьте, что заказы с этого склада Ozon должны попадать на
            выбранный склад МойСклад. Названия могут отличаться.
          </WarehouseInfo>
          <footer className="wh-drawer-footer">
            <div>
              <button className="secondary" onClick={close}>
                Отмена
              </button>
              <button
                className="mapping-primary"
                disabled={!candidate || !!confirmation.pending}
                onClick={() => setChecking(true)}
              >
                Проверить соответствие <Icon name="arrow" size="button" />
              </button>
            </div>
            <small>
              {confirmation.pending
                ? "Сначала уточните результат предыдущего подтверждения."
                : "Сначала проверим возможность сопоставления. Изменения пока не сохраняются."}
            </small>
          </footer>
        </section>
      )}
    </IntegrationDialog>
  );
}
