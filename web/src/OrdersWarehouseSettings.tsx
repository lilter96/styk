import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "./api";
import { Icon } from "./UI";
import {
  ordersURL,
  sameOrderScope,
  type NativeScope,
  type OrderScope,
} from "./OrdersTypes";

type WarehousePage = {
  scope: NativeScope;
  items: {
    ozon_id: string;
    ozon_name: string;
    store_id: string;
    store_name: string;
    status: string;
  }[];
  confirmed: number;
  total: number;
  next_cursor?: string;
};
function useWarehouses(scope: OrderScope, cursor = "") {
  return useQuery({
    queryKey: ["orders-warehouses", scope, cursor],
    queryFn: async ({ signal }) => {
      const result = await api<WarehousePage>(
        ordersURL("warehouses", scope, { cursor, limit: "25" }),
        signal,
      );
      if (
        !Array.isArray(result.items) ||
        !Number.isFinite(result.total) ||
        !Number.isFinite(result.confirmed) ||
        !sameOrderScope(result.scope, scope)
      )
        throw new Error("warehouse scope mismatch");
      return result;
    },
    retry: false,
    staleTime: 30000,
  });
}
export function WarehouseSummary({
  scope,
  onOpen,
}: {
  scope: OrderScope;
  onOpen: () => void;
}) {
  const query = useWarehouses(scope);
  return (
    <div className="settings-warehouse-summary">
      <Icon name="refresh" />
      <div>
        <strong>Склады выбираются по сопоставлениям</strong>
        <p>Для каждого склада Ozon используется связанный склад МойСклад.</p>
        <small>Общего или запасного склада для всех заказов нет.</small>
      </div>
      <div>
        <span className="settings-badge">
          {query.isLoading
            ? "Проверяем связи…"
            : query.isError
              ? "Не удалось проверить"
              : `${query.data?.confirmed ?? 0} из ${query.data?.total ?? 0} подтверждено`}
        </span>
        <button type="button" className="text-action" onClick={onOpen}>
          Посмотреть склады <Icon name="arrow" />
        </button>
      </div>
    </div>
  );
}
const statusLabels: Record<string, string> = {
  confirmed: "Подтверждено",
  missing: "Нет сопоставления",
  unconfirmed: "Требует подтверждения",
  stale: "Данные склада недоступны",
  conflict: "Конфликт связей",
};
export function OrdersWarehouseSettings({ scope }: { scope: OrderScope }) {
  const [cursors, setCursors] = useState([""]);
  const query = useWarehouses(scope, cursors.at(-1));
  return (
    <>
      <div className="settings-card">
        <div className="settings-card-title">
          <h3>Сопоставленные склады</h3>
          {query.data && (
            <span className="settings-badge">
              {query.data.confirmed} из {query.data.total}
            </span>
          )}
          <a className="settings-outline-link" href="/warehouses">
            Управлять сопоставлениями <Icon name="arrow" />
          </a>
        </div>
        {query.isLoading && (
          <p role="status">Загружаем сопоставления складов…</p>
        )}
        {query.isError && (
          <p role="alert">
            Не удалось загрузить склады.{" "}
            <button
              type="button"
              className="text-action"
              onClick={() => void query.refetch()}
            >
              Повторить
            </button>
          </p>
        )}
        {query.data && !query.data.items.length && (
          <p>
            Склады ещё не загружены. Обновите аккаунт в «Интеграциях», затем{" "}
            <a href="/warehouses">сопоставьте склады</a>.
          </p>
        )}
        {!!query.data?.items.length && (
          <div className="settings-warehouse-table">
            <table>
              <thead>
                <tr>
                  <th>Склад Ozon</th>
                  <th>Склад МойСклад</th>
                  <th>Состояние</th>
                </tr>
              </thead>
              <tbody>
                {query.data.items.map((item) => (
                  <tr key={item.ozon_id}>
                    <td>
                      <Icon name="warehouse" />
                      {item.ozon_name || "Название недоступно"}
                    </td>
                    <td>
                      <Icon name="warehouse" />
                      {item.store_name || "Не выбран"}
                    </td>
                    <td>
                      <span
                        className="settings-mapping-status"
                        data-status={item.status}
                      >
                        {item.status === "confirmed" && <Icon name="check" />}
                        {statusLabels[item.status] ?? "Требует проверки"}
                      </span>
                      {item.status !== "confirmed" && (
                        <a href="/warehouses">Сопоставить склад</a>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        {(cursors.length > 1 || query.data?.next_cursor) && (
          <div className="settings-pagination">
            <button
              type="button"
              className="secondary"
              disabled={query.isFetching || cursors.length === 1}
              onClick={() => setCursors(cursors.slice(0, -1))}
            >
              Назад
            </button>
            <button
              type="button"
              className="secondary"
              disabled={query.isFetching || !query.data?.next_cursor}
              onClick={() =>
                setCursors([...cursors, query.data?.next_cursor ?? ""])
              }
            >
              Далее
            </button>
          </div>
        )}
      </div>
      <p className="settings-info">
        <Icon name="error" />
        Новые сопоставления настраиваются в разделе «Склады» и используются и
        для остатков, и для заказов.
      </p>
      <div className="settings-card settings-missing-mapping">
        <Icon name="error" />
        <div>
          <h3>Если склад не сопоставлен</h3>
          <p>
            Затронутый заказ останется на проверке. STYK не отправит его на
            другой склад по умолчанию. Остальные заказы продолжат
            обрабатываться.
          </p>
        </div>
        <a href="/warehouses">
          Перейти к складам <Icon name="arrow" />
        </a>
      </div>
    </>
  );
}
