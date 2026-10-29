import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, post, params, ApiError, type Page, type Product } from "./api";
import type { Confirmation } from "./Confirmation";

type Scope = { tenant: string; moy: string; ozon: string };
type Target = {
  kind: string;
  id: string;
  name?: string;
  article: string;
  code: string;
  parent_id?: string;
};
type History = {
  mapping_id: number;
  version: number;
  product_id: string;
  offer_id: string;
  target_kind: string;
  target_id: string;
  provenance: string;
  confirmed_at: string;
  ozon_import_id: number;
  moysklad_import_id: number;
  evidence_class?: string;
  principal_type?: string;
  explanation?: string;
};
type Review = {
  Review: {
    ID: string;
    EvidenceClass: string;
    CreatedAt: string;
    ExpiresAt: string;
    OzonImportID: number;
    MoyskladImportID: number;
    ExistingMappingVersion: number | null;
    Source: { ID: string };
    Target: { ID: string; Kind: string };
  };
  Status: string;
  MappingConflict: boolean;
  PrincipalType: string;
};
export function ProductReview({
  product,
  scope,
  close,
  confirmation,
}: {
  product: Product;
  scope: Scope;
  close: () => void;
  confirmation: Confirmation;
}) {
  const client = useQueryClient();
  const [search, setSearch] = useState("");
  const [offset, setOffset] = useState(0);
  const [historyOffset, setHistoryOffset] = useState(0);
  const [selected, setSelected] = useState<Target | null>(null);
  const [evidence, setEvidence] = useState("OPERATOR_ATTESTED");
  const [reviewID, setReviewID] = useState("");
  const [creating, setCreating] = useState(false);
  const [reviewError, setReviewError] = useState("");
  const [acknowledge, setAcknowledge] = useState(false);
  const [explanation, setExplanation] = useState("");
  const base = {
    tenant: scope.tenant,
    moysklad_account: scope.moy,
    ozon_account: scope.ozon,
  };
  const targets = useQuery({
    queryKey: ["review-targets", scope, search, offset],
    queryFn: ({ signal }) =>
      api<Page<Target>>(
        "/api/moysklad-products?" + params({ ...base, search, offset }),
        signal,
      ),
  });
  const history = useQuery({
    queryKey: ["review-history", scope, product.product_id, historyOffset],
    queryFn: ({ signal }) =>
      api<Page<History>>(
        "/api/product-mapping-history?" +
          params({
            ...base,
            product: product.product_id,
            offset: historyOffset,
          }),
        signal,
      ),
  });
  const review = useQuery({
    queryKey: ["mapping-review", reviewID],
    queryFn: ({ signal }) =>
      api<Review>(`/api/product-mapping-reviews/${reviewID}`, signal),
    enabled: !!reviewID,
    staleTime: 0,
    refetchInterval: 5000,
    retry: false,
  });
  useEffect(() => {
    if (review.error instanceof ApiError && review.error.status === 401)
      void client.invalidateQueries({ queryKey: ["session"] });
  }, [review.error, client]);
  const candidate =
    selected &&
    product.candidates.find(
      (c) => c.kind === selected.kind && c.id === selected.id,
    );
  async function createReview() {
    if (!selected || creating || confirmation.pending) return;
    setCreating(true);
    setReviewError("");
    try {
      const result = await post<Review>("/api/product-mapping-reviews", {
        ...base,
        product_id: product.product_id,
        target_kind: selected.kind,
        target_id: selected.id,
        evidence_class: evidence,
      });
      setReviewID(result.Review.ID);
      setAcknowledge(false);
    } catch (err) {
      if (err instanceof ApiError && err.status === 401)
        void client.invalidateQueries({ queryKey: ["session"] });
      setReviewError(
        err instanceof ApiError && err.status === 401
          ? "Session expired. Sign in again."
          : "Review creation failed. Check the selected identities and account scope.",
      );
    } finally {
      setCreating(false);
    }
  }
  return (
    <section
      className="warehouse-panel product-review"
      aria-label="Product review"
    >
      <div className="heading-row">
        <h2>Product review</h2>
        <button className="secondary" onClick={close}>
          Close review
        </button>
      </div>
      <p className="review-source">
        Ozon offer: {product.offer_id} · product: {product.product_id} · SKU:{" "}
        {product.sku ?? "Unknown"} ·{" "}
        {product.archived === null
          ? "Archive status unknown"
          : product.archived
            ? "Archived"
            : "Active"}
      </p>
      <p className="review-status">
        Current matching result: {product.match_status}. Suggestions are not
        identity proof.
      </p>
      <h3>Deterministic candidates</h3>
      {product.candidates.length === 0 ? (
        <p>No candidates returned.</p>
      ) : (
        <ul className="review-candidates">
          {product.candidates.map((c) => (
            <li key={c.kind + c.id}>
              {c.kind} · {c.id} — evidence: {c.evidence.join(", ")}
              <button
                className="link-button"
                disabled={creating}
                onClick={() => {
                  setSearch(c.id);
                  setOffset(0);
                  setSelected(null);
                  setReviewID("");
                }}
              >
                Find candidate details
              </button>
            </li>
          ))}
        </ul>
      )}
      {product.candidates_truncated && (
        <p>
          Candidate list or search universe was truncated; uniqueness is not
          established.
        </p>
      )}
      <h3>Search MoySklad products and variants</h3>
      <label className="review-search">
        Search by name, article, code or identity
        <input
          placeholder="Search MoySklad products"
          disabled={creating}
          value={search}
          onChange={(e) => {
            setSearch(e.target.value);
            setOffset(0);
            setSelected(null);
            setReviewID("");
          }}
        />
      </label>
      {targets.isLoading ? (
        <p role="status">Loading products…</p>
      ) : targets.error ? (
        <p role="alert">Product search unavailable.</p>
      ) : targets.data?.items.length === 0 ? (
        <p>No products found.</p>
      ) : (
        <ul className="review-targets">
          {targets.data?.items.map((t) => (
            <li
              key={t.kind + t.id}
              className={
                selected?.kind === t.kind && selected?.id === t.id
                  ? "selected"
                  : ""
              }
            >
              <button
                className="review-target-button"
                aria-label={`${t.name ? `${t.name}, ` : ""}${t.kind} · ${t.id}`}
                aria-pressed={
                  selected?.kind === t.kind && selected?.id === t.id
                }
                disabled={creating}
                onClick={() => {
                  setSelected(t);
                  setReviewID("");
                  setEvidence("OPERATOR_ATTESTED");
                }}
              >
                <span className="review-target-name">
                  {t.name ||
                    (t.kind === "variant"
                      ? "Unnamed variant"
                      : "Unnamed product")}
                </span>
                <span className="review-target-id">
                  {t.kind} · {t.id}
                </span>
              </button>
              <div className="review-target-meta">
                Article: {t.article || "Unknown"} · Code: {t.code || "Unknown"}
                {t.kind === "variant" && (
                  <> · parent product: {t.parent_id || "Unknown"}</>
                )}
              </div>
            </li>
          ))}
        </ul>
      )}
      <div className="pagination">
        <button
          className="secondary"
          disabled={offset === 0}
          onClick={() => setOffset(Math.max(offset - 50, 0))}
        >
          Previous products
        </button>
        <button
          className="secondary"
          disabled={!targets.data?.has_next}
          onClick={() => setOffset(offset + 50)}
        >
          Next products
        </button>
      </div>
      {selected && (
        <div className="notice review-evidence">
          <strong>Evidence review</strong>
          <p>
            Ozon {product.offer_id} → MoySklad {selected.kind} {selected.id}
          </p>
          {selected.name && <p>Selected item: {selected.name}</p>}
          <p>
            {candidate
              ? `DETERMINISTIC_CANDIDATE: ${candidate.evidence.join(", ")}`
              : "No deterministic evidence for this selection."}
          </p>
          <p>
            Article: {selected.article || "Unknown"}; code:{" "}
            {selected.code || "Unknown"}. Import changes since review and
            independent identity verification are not established.
          </p>
          {selected.kind === "variant" && (
            <p>Parent product: {selected.parent_id || "Unknown"}</p>
          )}
          <p>
            By confirming, you attest that these products represent the same
            item. This relationship has not been independently verified.
          </p>
          <label className="review-field">
            Evidence classification{" "}
            <select
              disabled={creating}
              value={evidence}
              onChange={(e) => {
                setEvidence(e.target.value);
                setReviewID("");
              }}
            >
              <option value="OPERATOR_ATTESTED">OPERATOR_ATTESTED</option>
              {candidate && (
                <option value="DETERMINISTIC_CANDIDATE">
                  DETERMINISTIC_CANDIDATE
                </option>
              )}
            </select>
          </label>
          <button
            className="review-primary"
            disabled={creating || !!confirmation.pending}
            onClick={() => void createReview()}
          >
            {creating ? "Creating review…" : "Create review"}
          </button>
          {reviewError && <p role="alert">{reviewError}</p>}
          {reviewID &&
            (review.isLoading ? (
              <p role="status">Loading review…</p>
            ) : review.error ? (
              <p role="alert">
                Review unavailable. Sign in again if your session ended.
              </p>
            ) : (
              review.data && (
                <div className="review-confirmation">
                  <h3>Durable review</h3>
                  <p>
                    Ozon product {review.data.Review.Source.ID} → MoySklad{" "}
                    {review.data.Review.Target.Kind}{" "}
                    {review.data.Review.Target.ID}
                  </p>
                  <p>
                    {review.data.Review.EvidenceClass} · {review.data.Status} ·
                    created{" "}
                    <time dateTime={review.data.Review.CreatedAt}>
                      {new Date(review.data.Review.CreatedAt).toLocaleString()}
                    </time>{" "}
                    · expires{" "}
                    <time dateTime={review.data.Review.ExpiresAt}>
                      {new Date(review.data.Review.ExpiresAt).toLocaleString()}
                    </time>
                  </p>
                  <p>
                    Import references: Ozon {review.data.Review.OzonImportID},
                    MoySklad {review.data.Review.MoyskladImportID}.{" "}
                    {review.data.Review.ExistingMappingVersion !== null &&
                      `Existing mapping version: ${review.data.Review.ExistingMappingVersion}. `}
                    {review.data.MappingConflict &&
                      "Existing mapping conflict: confirmation is blocked."}
                  </p>
                  {review.data.Status !== "CURRENT" && (
                    <p>
                      Review expired or source/mapping changed. Create a new
                      review; the old revisions will not be refreshed.
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
                      checked={acknowledge}
                      onChange={(e) => setAcknowledge(e.target.checked)}
                    />{" "}
                    I explicitly confirm this relationship.
                  </label>
                  <button
                    className="review-primary"
                    disabled={
                      !acknowledge ||
                      !!confirmation.pending ||
                      review.data.Status !== "CURRENT" ||
                      confirmation.result?.review_id === reviewID ||
                      review.data.MappingConflict ||
                      Date.now() >= Date.parse(review.data.Review.ExpiresAt)
                    }
                    onClick={() => {
                      try {
                        confirmation.start(
                          scope,
                          product.product_id,
                          reviewID,
                          explanation,
                        );
                      } catch {
                        setReviewError(
                          "Explanation is invalid or exceeds 1,000 UTF-8 bytes.",
                        );
                      }
                    }}
                  >
                    Confirm mapping
                  </button>
                </div>
              )
            ))}
        </div>
      )}
      <h3>Mapping audit history</h3>
      {history.isLoading ? (
        <p role="status">Loading history…</p>
      ) : history.error ? (
        <p role="alert">Mapping history unavailable.</p>
      ) : history.data?.items.length === 0 ? (
        <p>No confirmed mapping history for this account pair.</p>
      ) : (
        <ul className="review-history">
          {history.data?.items.map((h) => (
            <li key={h.mapping_id + ":" + h.version}>
              Mapping {h.mapping_id}, version {h.version}: {h.offer_id} →{" "}
              {h.target_kind} {h.target_id};{" "}
              {new Date(h.confirmed_at).toLocaleString()}; provenance:{" "}
              {h.provenance}; import references {h.ozon_import_id} /{" "}
              {h.moysklad_import_id}
              {h.evidence_class && <>; evidence: {h.evidence_class}</>}
              {h.principal_type && <>; principal: {h.principal_type}</>}
              {h.explanation && <>; explanation: {h.explanation}</>}
            </li>
          ))}
        </ul>
      )}
      <div className="pagination">
        <button
          className="secondary"
          disabled={historyOffset === 0}
          onClick={() => setHistoryOffset(Math.max(historyOffset - 50, 0))}
        >
          Previous history
        </button>
        <button
          className="secondary"
          disabled={!history.data?.has_next}
          onClick={() => setHistoryOffset(historyOffset + 50)}
        >
          Next history
        </button>
      </div>
    </section>
  );
}
