import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api, params, type Page, type Product } from "./api";

type Scope = { tenant: string; moy: string; ozon: string };
type Target = { kind: string; id: string; article: string; code: string };
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
};
export function ProductReview({
  product,
  scope,
  close,
}: {
  product: Product;
  scope: Scope;
  close: () => void;
}) {
  const [search, setSearch] = useState("");
  const [offset, setOffset] = useState(0);
  const [historyOffset, setHistoryOffset] = useState(0);
  const [selected, setSelected] = useState<Target | null>(null);
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
  const candidate =
    selected &&
    product.candidates.find(
      (c) => c.kind === selected.kind && c.id === selected.id,
    );
  return (
    <section className="warehouse-panel" aria-label="Product review">
      <div className="heading-row">
        <h2>Product review</h2>
        <button className="secondary" onClick={close}>
          Close review
        </button>
      </div>
      <p>
        Ozon offer: {product.offer_id} · product: {product.product_id} · SKU:{" "}
        {product.sku ?? "Unknown"} ·{" "}
        {product.archived === null
          ? "Archive status unknown"
          : product.archived
            ? "Archived"
            : "Active"}
      </p>
      <p>
        Current matching result: {product.match_status}. Suggestions are not
        identity proof.
      </p>
      <h3>Deterministic candidates</h3>
      {product.candidates.length === 0 ? (
        <p>No candidates returned.</p>
      ) : (
        <ul>
          {product.candidates.map((c) => (
            <li key={c.kind + c.id}>
              {c.kind} · {c.id} — evidence: {c.evidence.join(", ")}
              <button
                className="link-button"
                onClick={() => {
                  setSearch(c.id);
                  setOffset(0);
                  setSelected(null);
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
      <label>
        Article, code or identity
        <input
          value={search}
          onChange={(e) => {
            setSearch(e.target.value);
            setOffset(0);
            setSelected(null);
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
        <ul>
          {targets.data?.items.map((t) => (
            <li key={t.kind + t.id}>
              <button className="link-button" onClick={() => setSelected(t)}>
                {t.kind} · {t.id}
              </button>{" "}
              · article: {t.article || "Unknown"} · code: {t.code || "Unknown"}
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
        <div className="notice">
          <strong>Evidence review</strong>
          <p>
            Ozon {product.offer_id} → MoySklad {selected.kind} {selected.id}
          </p>
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
          <p>
            A local operator selection is an attestation, not independently
            verified evidence. Confirmation is unavailable in this console; use
            the trusted administrative workflow only after independently
            verifying identity and reviewing the current imports.
          </p>
          <button disabled>Confirm mapping unavailable</button>
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
        <ul>
          {history.data?.items.map((h) => (
            <li key={h.mapping_id + ":" + h.version}>
              Mapping {h.mapping_id}, version {h.version}: {h.offer_id} →{" "}
              {h.target_kind} {h.target_id};{" "}
              {new Date(h.confirmed_at).toLocaleString()}; provenance:{" "}
              {h.provenance}; import references {h.ozon_import_id} /{" "}
              {h.moysklad_import_id}
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
