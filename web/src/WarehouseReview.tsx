import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, post, params, ApiError } from "./api";
import { useConfirmation, type Confirmation } from "./Confirmation";

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
type DurableReview = {
  review: {
    id: string;
    tenant: string;
    moysklad_account: string;
    ozon_account: string;
    moysklad_warehouse_id: string;
    ozon_warehouse_id: string;
    moysklad_import_id: number;
    ozon_import_id: number;
    created_at: string;
    expires_at: string;
    existing_mapping_id: number | null;
    existing_mapping_version: number | null;
  };
  status: string;
  principal_type: string;
};
export function WarehouseReview({
  scope,
  moy,
  ozon,
  confirmation,
}: {
  scope: Scope;
  moy: string;
  ozon: string;
  confirmation?: Confirmation;
}) {
  const client = useQueryClient();
  const local = useConfirmation();
  const active = confirmation ?? local;
  const [offset, setOffset] = useState(0);
  const [reviewID, setReviewID] = useState("");
  const [creating, setCreating] = useState(false);
  const [error, setError] = useState("");
  const [ack, setAck] = useState(false);
  const [explanation, setExplanation] = useState("");
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
  const durable = useQuery({
    queryKey: ["warehouse-durable-review", reviewID],
    queryFn: ({ signal }) =>
      api<DurableReview>(`/api/warehouse-mapping-reviews/${reviewID}`, signal),
    enabled: !!reviewID,
    staleTime: 0,
    refetchInterval: 5000,
    retry: false,
  });
  useEffect(() => {
    if (durable.error instanceof ApiError && durable.error.status === 401)
      void client.invalidateQueries({ queryKey: ["session"] });
  }, [durable.error, client]);
  const eligible =
    q.data?.status === "ELIGIBLE_FOR_REVIEW" &&
    q.data.moysklad?.id === moy &&
    q.data.moysklad.account === scope.moy &&
    q.data.ozon?.id === ozon &&
    q.data.ozon.account === scope.ozon &&
    q.data.ozon.is_rfbs === false;
  const reviewed = durable.data?.review;
  const reviewMatchesSelection =
    reviewed?.id === reviewID &&
    reviewed.tenant === scope.tenant &&
    reviewed.moysklad_account === scope.moy &&
    reviewed.ozon_account === scope.ozon &&
    reviewed.moysklad_warehouse_id === moy &&
    reviewed.ozon_warehouse_id === ozon &&
    reviewed.moysklad_import_id > 0 &&
    reviewed.ozon_import_id > 0 &&
    durable.data?.principal_type === "LOCAL_OPERATOR_SESSION";
  async function createReview() {
    if (creating || active.pending || !eligible) return;
    setCreating(true);
    setError("");
    try {
      const result = await post<DurableReview>(
        "/api/warehouse-mapping-reviews",
        {
          tenant: scope.tenant,
          moysklad_account: scope.moy,
          ozon_account: scope.ozon,
          moysklad_warehouse_id: moy,
          ozon_warehouse_id: ozon,
        },
      );
      setReviewID(result.review.id);
      setAck(false);
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) {
        void client.invalidateQueries({ queryKey: ["session"] });
        setError("Session expired. Sign in again.");
      } else if (
        e instanceof ApiError &&
        e.code === "WAREHOUSE_ALREADY_ASSIGNED"
      ) {
        setError(
          "A warehouse is already assigned. Refresh the selection; restricted history is not disclosed.",
        );
        void client.invalidateQueries({ queryKey: ["warehouse-review"] });
      } else
        setError(
          "Could not create review. Refresh the source evidence and check the account scope.",
        );
    } finally {
      setCreating(false);
    }
  }
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
              By confirming, you attest that these warehouses correspond for the
              selected integration. This does not independently verify physical
              warehouse identity or authorize stock synchronization.
            </p>
            <button
              className="review-primary"
              disabled={creating || !!active.pending || !eligible}
              onClick={() => void createReview()}
            >
              {creating ? "Creating review…" : "Create warehouse review"}
            </button>
            {error && <p role="alert">{error}</p>}
            {reviewID &&
              (durable.isLoading ? (
                <p role="status">Loading durable review…</p>
              ) : durable.error ? (
                <p role="alert">
                  Review unavailable. Sign in again if your session ended.
                </p>
              ) : (
                durable.data && (
                  <div className="notice review-confirmation">
                    <h3>Durable warehouse review</h3>
                    <p>Review ID: {durable.data.review.id}</p>
                    <p>
                      MoySklad {durable.data.review.moysklad_warehouse_id} →
                      Ozon {durable.data.review.ozon_warehouse_id}
                    </p>
                    <p>
                      Status: {durable.data.status}; created{" "}
                      <time dateTime={durable.data.review.created_at}>
                        {new Date(
                          durable.data.review.created_at,
                        ).toLocaleString()}
                      </time>
                      ; expires{" "}
                      <time dateTime={durable.data.review.expires_at}>
                        {new Date(
                          durable.data.review.expires_at,
                        ).toLocaleString()}
                      </time>
                      .
                    </p>
                    <p>
                      Import references: MoySklad{" "}
                      {durable.data.review.moysklad_import_id}; Ozon{" "}
                      {durable.data.review.ozon_import_id}.{" "}
                      {durable.data.review.existing_mapping_id !== null &&
                        "An existing mapping prevents a new confirmation."}
                    </p>
                    {durable.data.status !== "CURRENT" && (
                      <p>
                        Review expired or source/mapping changed. Create a new
                        review; the recorded revisions will not be refreshed.
                      </p>
                    )}
                    {!reviewMatchesSelection && (
                      <p role="alert">
                        The review does not match the selected scope and
                        evidence. Refresh and create a new review.
                      </p>
                    )}
                    <label className="review-field">
                      Optional explanation (no credentials or customer data)
                      <input
                        value={explanation}
                        onChange={(e) => setExplanation(e.target.value)}
                        maxLength={1000}
                      />
                    </label>
                    <label className="review-acknowledge">
                      <input
                        type="checkbox"
                        checked={ack}
                        onChange={(e) => setAck(e.target.checked)}
                      />{" "}
                      I explicitly confirm this warehouse relationship.
                    </label>
                    <button
                      className="review-primary"
                      disabled={
                        !ack ||
                        !!active.pending ||
                        durable.data.status !== "CURRENT" ||
                        durable.data.review.existing_mapping_id !== null ||
                        active.result?.review_id === reviewID ||
                        active.failure?.reviewId === reviewID ||
                        !Number.isFinite(
                          Date.parse(durable.data.review.expires_at),
                        ) ||
                        Date.now() >=
                          Date.parse(durable.data.review.expires_at) ||
                        !eligible ||
                        !reviewMatchesSelection
                      }
                      onClick={() => {
                        try {
                          active.start(
                            scope,
                            `${moy} → ${ozon}`,
                            reviewID,
                            explanation,
                            "warehouse",
                          );
                        } catch {
                          setError(
                            "Explanation is invalid or exceeds 1,000 UTF-8 bytes.",
                          );
                        }
                      }}
                    >
                      Confirm warehouse mapping
                    </button>
                  </div>
                )
              ))}
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
