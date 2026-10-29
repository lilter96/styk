import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api, params } from "./api";

type Scope = { tenant: string; moy: string; ozon: string };
type Evidence = {
  id: string;
  name: string;
  account: string;
  status: string;
  is_rfbs: boolean | null;
  observed_at: string;
  stock_rows: number;
};
type Mapping = {
  mapping_id: number;
  version: number;
  moysklad_warehouse_id: string;
  ozon_warehouse_id: string;
  provenance: string;
  confirmed_at: string;
};
type Review = {
  status: string;
  moysklad: Evidence | null;
  ozon: Evidence | null;
  mappings: Mapping[];
  history: Mapping[];
  has_next: boolean;
  offset: number;
};
export function WarehouseReview({
  scope,
  moy,
  ozon,
}: {
  scope: Scope;
  moy: string;
  ozon: string;
}) {
  const [offset, setOffset] = useState(0);
  const q = useQuery({
    queryKey: ["warehouse-review", scope, moy, ozon, offset],
    queryFn: ({ signal }) =>
      api<Review>(
        "/api/warehouse-mapping-review?" +
          params({
            tenant: scope.tenant,
            moysklad_account: scope.moy,
            ozon_account: scope.ozon,
            moysklad_warehouse: moy,
            ozon_warehouse: ozon,
            offset,
          }),
        signal,
      ),
    retry: false,
  });
  const describe = (e: Evidence | null, source: string) =>
    e ? (
      <div className="warehouse-review-evidence">
        <h3>{source}</h3>
        <strong>{e.name || "Unnamed warehouse"}</strong>
        <p>Warehouse ID: {e.id}</p>
        <p>Account: {e.account}</p>
        {source === "Ozon" && (
          <p>
            Classification:{" "}
            {e.is_rfbs === null ? "Unknown" : e.is_rfbs ? "rFBS" : "FBS"} ·
            Status: {e.status || "Unknown"}
          </p>
        )}
        <p>
          Observed: {new Date(e.observed_at).toLocaleString()}{" "}
          {Date.parse(e.observed_at) > Date.now() &&
            "· Future timestamp; age unknown"}
          {Date.now() - Date.parse(e.observed_at) > 3600000 &&
            "· Stale observation"}
        </p>
        <p>
          Stock rows: {e.stock_rows}{" "}
          {e.stock_rows === 0 && "· No observation; stock is unknown"}
        </p>
      </div>
    ) : (
      <p>{source}: no eligible observation in the latest import.</p>
    );
  return (
    <section
      className="warehouse-panel warehouse-review"
      aria-label="Warehouse mapping review"
    >
      <h2>Warehouse mapping review</h2>
      {q.isLoading ? (
        <p role="status">Loading warehouse review…</p>
      ) : q.error ? (
        <p role="alert">
          Warehouse review unavailable. Check the account scope and try again.
        </p>
      ) : (
        q.data && (
          <>
            <p>
              <strong>{q.data.status}</strong>
            </p>
            <div className="warehouse-grid">
              {describe(q.data.moysklad, "MoySklad")}
              {describe(q.data.ozon, "Ozon")}
            </div>
            <p>
              Selection does not prove physical identity or stock ownership.
              Only one MoySklad warehouse and one Ozon FBS warehouse are
              supported; rFBS is unsupported.
            </p>
            <p>
              Confirmation is unavailable in this version. A future confirmation
              must revalidate the selected imports, conflicts and operator
              authorization.
            </p>
            <h3>Existing mappings affecting this pair</h3>
            {q.data.mappings.length ? (
              <ul>
                {q.data.mappings.map((m) => (
                  <li key={m.mapping_id}>
                    Mapping {m.mapping_id}, version {m.version}:{" "}
                    {m.moysklad_warehouse_id} → {m.ozon_warehouse_id}
                  </li>
                ))}
              </ul>
            ) : q.data.status === "MAPPING_CONFLICT" ? (
              <p>
                A conflicting mapping exists, but its history is outside the
                permitted scope.
              </p>
            ) : (
              <p>No confirmed mappings affect this pair.</p>
            )}
            <h3>Mapping history</h3>
            {q.data.history.length ? (
              <ul>
                {q.data.history.map((m) => (
                  <li key={`${m.mapping_id}:${m.version}`}>
                    Mapping {m.mapping_id}, version {m.version}:{" "}
                    {m.moysklad_warehouse_id} → {m.ozon_warehouse_id};{" "}
                    {new Date(m.confirmed_at).toLocaleString()}; provenance:{" "}
                    {m.provenance}
                  </li>
                ))}
              </ul>
            ) : (
              <p>No mapping history is visible for these warehouses.</p>
            )}
            <div className="pagination">
              <button
                className="secondary"
                disabled={!offset}
                onClick={() => setOffset(Math.max(0, offset - 50))}
              >
                Previous history
              </button>
              <button
                className="secondary"
                disabled={!q.data.has_next}
                onClick={() => setOffset(offset + 50)}
              >
                Next history
              </button>
            </div>
          </>
        )
      )}
    </section>
  );
}
