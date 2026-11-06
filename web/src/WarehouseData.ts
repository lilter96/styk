import { useQuery } from "@tanstack/react-query";
import { api, params } from "./api";
import type { Scope } from "./Confirmation";

export type WarehouseRow = {
  id: string;
  name: string;
  is_rfbs: boolean | null;
  warehouse_type: string;
  store_id: string;
  store_name: string;
  state: string;
  stock_rows: number;
  observed_at: string | null;
  mapping_id: number | null;
  confirmed_at: string | null;
};
export type WarehousePage = {
  items: WarehouseRow[];
  counts: {
    total: number;
    mapped: number;
    unmapped: number;
    attention: number;
  };
  total: number;
  offset: number;
  has_next: boolean;
  ozon_imported: boolean;
  moy_imported: boolean;
};
export type Candidate = { id: string; name: string; available: boolean };
export type CandidatePage = {
  items: Candidate[];
  total: number;
  offset: number;
  has_next: boolean;
};
export function warehouseURL(
  scope: Scope,
  extra: Record<string, string | number>,
) {
  return (
    "/api/warehouses?" +
    params({
      tenant: scope.tenant,
      moysklad_account: scope.moy,
      ozon_account: scope.ozon,
      ...extra,
    })
  );
}
export function useWarehouseOverview(
  scope: Scope,
  extra: Record<string, string | number>,
) {
  return useQuery({
    queryKey: ["warehouse-overview", scope, extra],
    queryFn: ({ signal }) =>
      api<WarehousePage>(
        warehouseURL(scope, { view: "overview", ...extra }),
        signal,
      ),
    enabled: !!scope.tenant && !!scope.moy && !!scope.ozon,
    retry: false,
  });
}
export function warehouseType(
  row: Pick<WarehouseRow, "is_rfbs" | "warehouse_type">,
) {
  if (row.is_rfbs === null) return "Неизвестен";
  if (row.is_rfbs)
    return row.warehouse_type === "rfbs_express" ? "rFBS Express" : "rFBS";
  return !row.warehouse_type || row.warehouse_type === "fbs"
    ? "FBS"
    : "Неизвестен";
}
export const warehouseStates: Record<string, string> = {
  mapped: "Сопоставлен",
  unmapped: "Не сопоставлен",
  conflict: "Конфликт связей",
  unsupported: "Недоступен для сопоставления",
  missing_evidence: "Требует проверки",
};
// The existing console uses a one-hour observation window. Future timestamps
// and invalid dates never qualify as fresh; refresh does not change this clock.
export function stockAge(date: string | null, rows = 1, now = Date.now()) {
  if (!rows) return { label: "Нет данных", state: "missing" };
  const time = date ? Date.parse(date) : NaN;
  if (!Number.isFinite(time))
    return { label: "Время неизвестно", state: "unknown" };
  if (time > now) return { label: "Время указано в будущем", state: "unknown" };
  if (now - time > 3600000) return { label: "Устарели", state: "stale" };
  return { label: "Получены", state: "received" };
}
export function warehouseHref(id: string, search = "") {
  return `/warehouses/${encodeURIComponent(id)}${search}`;
}
