import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import { Icon, StatePanel } from "./UI";
import { ApiError } from "./api";
import { BrandLogo } from "./BrandLogo";
import {
  stockAge,
  warehouseStates,
  warehouseType,
  type WarehouseRow,
} from "./WarehouseData";

export function WarehouseStatus({ state }: { state: string }) {
  return (
    <span className="wh-status" data-state={state}>
      <Icon name={state === "mapped" ? "check" : "error"} size="button" />
      {warehouseStates[state] ?? "Состояние неизвестно"}
    </span>
  );
}
export function WarehouseType({
  row,
}: {
  row: Pick<WarehouseRow, "is_rfbs" | "warehouse_type">;
}) {
  return <span className="wh-type">{warehouseType(row)}</span>;
}
export function StockAge({
  date,
  rows = 1,
}: {
  date: string | null;
  rows?: number;
}) {
  const age = stockAge(date, rows);
  return (
    <span className="wh-age" data-state={age.state}>
      <Icon
        name={
          age.state === "received"
            ? "check"
            : age.state === "missing"
              ? "more"
              : "history"
        }
        size="button"
      />
      <span>
        {age.label}
        {date && (
          <time dateTime={date}>
            {Number.isFinite(Date.parse(date))
              ? new Date(date).toLocaleString("ru-RU")
              : "Некорректная дата"}
          </time>
        )}
      </span>
    </span>
  );
}
export function WarehouseInfo({ children }: { children: ReactNode }) {
  return (
    <div className="wh-info">
      <Icon name="error" />
      <span>{children}</span>
    </div>
  );
}
export function WarehouseFailure({
  error,
  retry,
}: {
  error: unknown;
  retry: () => void;
}) {
  const lost = error instanceof ApiError && error.status === 401;
  return (
    <StatePanel kind="error">
      <div>
        <strong>
          {lost ? "Сессия завершилась" : "Не удалось получить данные"}
        </strong>
        <p>
          {lost
            ? "Войдите снова, чтобы продолжить работу со складами."
            : error instanceof ApiError && error.status === 404
              ? "Склад или выбранные аккаунты недоступны. Проверьте подключение в «Интеграциях»."
              : "Проверьте соединение и повторите запрос."}
        </p>
        <button className="secondary" onClick={retry}>
          Повторить запрос
        </button>
      </div>
    </StatePanel>
  );
}
export function WarehousePagination({
  offset,
  size,
  count,
  more,
  busy,
  change,
}: {
  offset: number;
  size: number;
  count: number;
  more: boolean;
  busy: boolean;
  change: (value: number) => void;
}) {
  return (
    <div className="wh-pagination">
      <span>
        {count ? `Показаны ${offset + 1}–${offset + count}` : "Нет строк"}
      </span>
      <div>
        <button
          className="secondary"
          aria-label="Предыдущая страница"
          disabled={!offset || busy}
          onClick={() => change(Math.max(0, offset - size))}
        >
          <Icon name="chevron-left" size="button" />
        </button>
        <span aria-label="Текущая страница">
          {Math.floor(offset / size) + 1}
        </span>
        <button
          className="secondary"
          aria-label="Следующая страница"
          disabled={!more || busy}
          onClick={() => change(offset + size)}
        >
          <Icon name="chevron-right" size="button" />
        </button>
      </div>
    </div>
  );
}
export function WarehousePair({
  ozonName,
  storeName,
}: {
  ozonName: string;
  storeName: string;
}) {
  return (
    <div className="wh-pair">
      <div>
        <BrandLogo provider="ozon" decorative />
        <span>
          <strong>{ozonName || "Название склада недоступно"}</strong>
          <small>Ozon</small>
        </span>
      </div>
      <Icon name="arrow" />
      <div>
        <BrandLogo provider="moysklad" decorative />
        <span>
          <strong>{storeName || "Название склада недоступно"}</strong>
          <small>МойСклад</small>
        </span>
      </div>
    </div>
  );
}
export function WarehouseEmpty({
  search = false,
  imported = false,
}: {
  search?: boolean;
  imported?: boolean;
}) {
  return (
    <StatePanel>
      <div>
        <Icon name="warehouse" size="metric" />
        <h3>
          {search
            ? "Склады не найдены"
            : imported
              ? "В аккаунте пока нет складов"
              : "Склады ещё не загружены"}
        </h3>
        <p>
          {search
            ? "Измените поиск или выберите другой фильтр."
            : imported
              ? "Последняя загрузка Ozon завершена. Доступных складов в ней нет."
              : "Проверьте подключение и загрузку данных выбранных аккаунтов."}
        </p>
        {!search && <Link to="/integrations">Перейти в «Интеграции»</Link>}
      </div>
    </StatePanel>
  );
}
