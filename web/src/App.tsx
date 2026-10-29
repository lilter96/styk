import { Integrations } from "./Integrations";
import {
  ImportSubmissionNotice,
  useImportSubmission,
} from "./ImportSubmission";
import { ProductReview } from "./ProductReview";
import { WarehouseReview } from "./WarehouseReview";
import {
  ConfirmationNotice,
  useConfirmation,
  type Confirmation,
} from "./Confirmation";
import { useEffect, useState, type FormEvent } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { NavLink, Route, Routes, useNavigate } from "react-router-dom";
import {
  api,
  params,
  type Account,
  type Batch,
  type Page,
  type Product,
  type Report,
  type Stock,
  type Warehouse,
} from "./api";

type Scope = { tenant: string; moy: string; ozon: string };
const text = (s: string) => s.toLowerCase().replaceAll("_", " ");
function State({ status }: { status: string }) {
  return (
    <span className={"state " + status.toLowerCase()}>
      <span className="dot" />
      {text(status)}
    </span>
  );
}
function Notice({ children }: { children: React.ReactNode }) {
  return <div className="notice">{children}</div>;
}
function Loading() {
  return (
    <div className="notice" role="status">
      Loading observations…
    </div>
  );
}
function Failure({ error }: { error: unknown }) {
  return (
    <div className="error" role="alert">
      {error instanceof Error ? error.message : "The request failed."}
    </div>
  );
}
function scopeParams(s: Scope) {
  return params({
    tenant: s.tenant,
    moysklad_account: s.moy,
    ozon_account: s.ozon,
  });
}

function Login({ onDone }: { onDone: () => void }) {
  const [secret, setSecret] = useState(""),
    [error, setError] = useState(""),
    [busy, setBusy] = useState(false);
  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      const r = await fetch("/api/session", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ secret }),
      });
      setSecret("");
      if (!r.ok)
        throw new Error(
          r.status === 401
            ? "That operator secret was not accepted."
            : "Unable to sign in. Check the local API.",
        );
      onDone();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Unable to sign in.");
    } finally {
      setBusy(false);
    }
  }
  return (
    <main className="login">
      <div className="login-mark">
        S<span>TYK</span>
      </div>
      <div className="login-panel">
        <p className="eyebrow">Local operator console</p>
        <h1>See the whole connection.</h1>
        <p>
          Inspect integration evidence and explicitly confirm product mappings.
          Marketplace data is read-only.
        </p>
        <form onSubmit={submit}>
          <label htmlFor="secret">Operator secret</label>
          <input
            id="secret"
            type="password"
            autoComplete="off"
            value={secret}
            onChange={(e) => setSecret(e.target.value)}
            required
          />
          <button disabled={busy}>{busy ? "Signing in…" : "Sign in"}</button>
          {error && (
            <div className="error" role="alert">
              {error}
            </div>
          )}
        </form>
      </div>
      <p className="login-footer">STYK · Local workspace</p>
    </main>
  );
}
function SourceCard({
  name,
  batch,
  status,
}: {
  name: string;
  batch?: Batch;
  status: string;
}) {
  return (
    <article className="source-card">
      <div className="source-top">
        <div>
          <div className="source-symbol">{name === "MoySklad" ? "M" : "O"}</div>
          <h3>{name}</h3>
        </div>
        <State status={status} />
      </div>
      <p className="source-caption">
        {batch?.present
          ? batch.completed
            ? "Completed import"
            : "Incomplete import"
          : "No import available"}
      </p>
      <div className="stats">
        <div>
          <strong>{batch?.counts?.products ?? "—"}</strong>
          <span>Products</span>
        </div>
        {name === "MoySklad" && (
          <div>
            <strong>{batch?.counts?.variants ?? "—"}</strong>
            <span>Variants</span>
          </div>
        )}
        <div>
          <strong>{batch?.counts?.warehouses ?? "—"}</strong>
          <span>Warehouses</span>
        </div>
        <div>
          <strong>{batch?.counts?.stock_rows ?? "—"}</strong>
          <span>Stock rows</span>
        </div>
      </div>
      {batch?.present &&
        batch.observed_at &&
        Date.now() - Date.parse(batch.observed_at) > 3600000 && (
          <p className="source-caption">Stale observation</p>
        )}
      <div className="card-foot">
        {batch?.present
          ? batch.age?.startsWith("-")
            ? "Observation timestamp is in the future"
            : `Observed ${batch.age} ago`
          : "No observation time"}
        <span>Live connection not checked</span>
        {batch?.present && batch.observed_at && (
          <time dateTime={batch.observed_at}>
            Imported {new Date(batch.observed_at).toLocaleString()}
          </time>
        )}
      </div>
    </article>
  );
}
export function Overview({
  report,
  loading,
  error,
}: {
  report?: Report;
  loading: boolean;
  error: unknown;
}) {
  if (loading) return <Loading />;
  if (error) return <Failure error={error} />;
  if (!report)
    return (
      <Notice>
        Select both registered accounts to see their observations.
      </Notice>
    );
  return (
    <>
      <div className="heading-row">
        <div>
          <p className="eyebrow">Your workspace</p>
          <h1>Integration overview</h1>
          <p className="subtitle">
            A current view of the evidence STYK has collected.
          </p>
        </div>
        <span className="readonly">
          <i />
          Marketplace writes disabled
        </span>
      </div>
      <section className="intro">
        <div>
          <span className="intro-label">Synchronization readiness</span>
          <h2>Evidence before action.</h2>
          <p>
            Imports show what each system reported. They do not establish that
            stock can be safely published.
          </p>
        </div>
        <div className="intro-status">
          <span>Publication</span>
          <strong>Disabled</strong>
          <small>Safety checks remain open</small>
        </div>
      </section>
      <div className="section-heading">
        <div>
          <h2>Source observations</h2>
          <p>Latest persisted imports for your selected accounts.</p>
        </div>
        <NavLink to="/diagnostics">View diagnostics</NavLink>
      </div>
      <div className="sources">
        <SourceCard
          name="MoySklad"
          batch={report.batches.moysklad}
          status={
            report.checks.find((c) => c.code === "MOYSKLAD_IMPORT")?.status ??
            "NOT_CHECKED"
          }
        />
        <SourceCard
          name="Ozon"
          batch={report.batches.ozon}
          status={
            report.checks.find((c) => c.code === "OZON_IMPORT")?.status ??
            "NOT_CHECKED"
          }
        />
      </div>
      <section className="bottom-note" aria-label="Next actions">
        <div>
          <strong>Next actions</strong>
          <p>
            Review unmatched products and unmapped warehouses; inspect missing
            stock evidence before considering synchronization.
          </p>
          <NavLink to="/products">Review products</NavLink>
          {" · "}
          <NavLink to="/warehouses">Review warehouses and stock</NavLink>
          {" · "}
          <NavLink to="/diagnostics">Inspect diagnostics</NavLink>
        </div>
      </section>
      <div className="bottom-note">
        <span className="info-icon">i</span>
        <div>
          <strong>Imported is not the same as connected</strong>
          <p>
            Availability and full source permissions are not checked by this
            view.
          </p>
        </div>
      </div>
    </>
  );
}
export function Products({
  scope,
  confirmation,
}: {
  scope: Scope;
  confirmation?: Confirmation;
}) {
  const localConfirmation = useConfirmation();
  const activeConfirmation = confirmation ?? localConfirmation;
  const [review, setReview] = useState<Product | null>(null);
  const [search, setSearch] = useState(""),
    [archived, setArchived] = useState(""),
    [mapping, setMapping] = useState(""),
    [offset, setOffset] = useState(0);
  const q = useQuery({
    queryKey: ["products", scope, search, archived, mapping, offset],
    queryFn: ({ signal }) =>
      api<Page<Product>>(
        "/api/products?" +
          params({
            ...{
              tenant: scope.tenant,
              moysklad_account: scope.moy,
              ozon_account: scope.ozon,
            },
            search,
            archived,
            mapping,
            offset,
          }),
        signal,
      ),
    enabled: !!(scope.tenant && scope.moy && scope.ozon),
  });
  return (
    <>
      <div className="heading-row">
        <div>
          <p className="eyebrow">Identity</p>
          <h1>Products</h1>
          <p className="subtitle">
            Review observations and confirmed relationships.
          </p>
        </div>
        <span className="readonly">
          <i />
          Local mapping review
        </span>
      </div>
      {review && (
        <ProductReview
          key={review.product_id}
          product={
            q.data?.items.find(
              (item) => item.product_id === review.product_id,
            ) ?? review
          }
          scope={scope}
          close={() => setReview(null)}
          confirmation={activeConfirmation}
        />
      )}
      <div className="toolbar">
        <label>
          <span>Search</span>
          <input
            placeholder="Offer ID, product ID or SKU"
            value={search}
            onChange={(e) => {
              setSearch(e.target.value);
              setOffset(0);
            }}
          />
        </label>
        <label>
          <span>Availability</span>
          <select
            value={archived}
            onChange={(e) => {
              setArchived(e.target.value);
              setOffset(0);
            }}
          >
            <option value="">All products</option>
            <option value="false">Active</option>
            <option value="true">Archived</option>
          </select>
        </label>
        <label>
          <span>Mapping</span>
          <select
            value={mapping}
            onChange={(e) => {
              setMapping(e.target.value);
              setOffset(0);
            }}
          >
            <option value="">All mappings</option>
            <option value="confirmed">Confirmed</option>
            <option value="unconfirmed">Unconfirmed</option>
          </select>
        </label>
      </div>
      {!scope.moy || !scope.ozon ? (
        <Notice>Select both accounts to view products.</Notice>
      ) : q.isLoading ? (
        <Loading />
      ) : q.error ? (
        <Failure error={q.error} />
      ) : !q.data?.items.length ? (
        <Notice>
          {mapping === "confirmed" && !search && !archived && offset === 0
            ? "No products are currently mapped between these accounts. Confirmation requires explicit identity evidence."
            : "No product observations match these filters."}
        </Notice>
      ) : (
        <>
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>Seller offer</th>
                  <th>Product ID</th>
                  <th>SKU</th>
                  <th>Status</th>
                  <th>Matching</th>
                </tr>
              </thead>
              <tbody>
                {q.data.items.map((p) => (
                  <tr key={p.product_id}>
                    <td className="primary-cell">
                      {p.offer_id}
                      <button
                        className="link-button"
                        onClick={() => setReview(p)}
                      >
                        Review
                      </button>
                    </td>
                    <td>{p.product_id}</td>
                    <td>{p.sku ?? "Unknown"}</td>
                    <td>
                      {p.archived === null
                        ? "Unknown"
                        : p.archived
                          ? "Archived"
                          : "Active"}
                    </td>
                    <td>
                      <strong>{text(p.match_status)}</strong>
                      {p.target_id && (
                        <small>
                          Mapped: {p.target_kind} · {p.target_id}
                        </small>
                      )}
                      {p.candidate_count > 0 && (
                        <details>
                          <summary>{p.candidate_count} candidates</summary>
                          {p.candidates.map((c) => (
                            <div key={c.kind + c.id}>
                              {c.kind}: {c.id} · {c.evidence.join(", ")}
                            </div>
                          ))}
                        </details>
                      )}
                      {p.candidates_truncated && (
                        <small>
                          Candidate list or source universe truncated
                        </small>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <div className="pagination">
            <button
              className="secondary"
              disabled={offset === 0}
              onClick={() => setOffset(Math.max(0, offset - 50))}
            >
              Previous
            </button>
            <span>
              Rows {offset + 1}–{offset + q.data.items.length}
            </span>
            <button
              className="secondary"
              disabled={!q.data.has_next}
              onClick={() => setOffset(offset + 50)}
            >
              Next
            </button>
          </div>
          <Notice>
            Suggestions are not confirmed mappings. Confirmation requires
            explicit identity evidence.
          </Notice>
        </>
      )}
    </>
  );
}
export function StockRows({
  scope,
  source,
  account,
  warehouse,
}: {
  scope: Scope;
  source: string;
  account: string;
  warehouse: string;
}) {
  const [offset, setOffset] = useState(0);
  const q = useQuery({
    queryKey: ["stock", scope.tenant, source, account, warehouse, offset],
    queryFn: ({ signal }) =>
      api<Page<Stock>>(
        "/api/stock?" +
          params({ tenant: scope.tenant, source, account, warehouse, offset }),
        signal,
      ),
  });
  if (q.isLoading) return <Loading />;
  if (q.error) return <Failure error={q.error} />;
  if (!q.data?.items.length)
    return <Notice>No stock observation. The quantity is unknown.</Notice>;
  const value = (x: string | null) => (x === null ? "Unknown" : x);
  return (
    <>
      <div className="stock-list">
        {q.data.items.map((s) => (
          <div className="stock-row" key={s.kind + s.item_id}>
            <strong>
              {s.kind} · {s.item_id}
            </strong>
            <small>
              {source === "moysklad"
                ? `Physical ${value(s.stock)} · Reserved ${value(s.reserve)} · Incoming ${value(s.incoming)} · Available ${value(s.quantity)}`
                : `Present ${value(s.present)} · Reserved ${value(s.reserved)} · Free stock ${value(s.free_stock)}`}
            </small>
            <small>
              {Date.now() - Date.parse(s.observed_at) > 3600000
                ? "Stale observation"
                : "Observed"}{" "}
              · {new Date(s.observed_at).toLocaleString()}
            </small>
          </div>
        ))}
      </div>
      <div className="pagination">
        <button
          className="secondary"
          disabled={offset === 0}
          onClick={() => setOffset(Math.max(0, offset - 50))}
        >
          Previous
        </button>
        <span>
          Rows {offset + 1}–{offset + q.data.items.length}
        </span>
        <button
          className="secondary"
          disabled={!q.data.has_next}
          onClick={() => setOffset(offset + 50)}
        >
          Next
        </button>
      </div>
    </>
  );
}
function WarehouseList({
  scope,
  source,
  account,
  other,
  selectWarehouse,
}: {
  scope: Scope;
  source: string;
  account: string;
  other: string;
  selectWarehouse: (id: string) => void;
}) {
  const [offset, setOffset] = useState(0),
    [selected, setSelected] = useState("");
  const q = useQuery({
    queryKey: ["warehouses", scope.tenant, source, account, other, offset],
    queryFn: ({ signal }) =>
      api<Page<Warehouse>>(
        "/api/warehouses?" +
          params({
            tenant: scope.tenant,
            source,
            account,
            other_account: other,
            offset,
          }),
        signal,
      ),
    enabled: !!scope.tenant && !!account,
  });
  return (
    <section className="warehouse-panel">
      <h2>{source === "moysklad" ? "MoySklad" : "Ozon"}</h2>
      {!account ? (
        <Notice>No account selected.</Notice>
      ) : q.isLoading ? (
        <Loading />
      ) : q.error ? (
        <Failure error={q.error} />
      ) : !q.data?.items.length ? (
        <Notice>No warehouse observations.</Notice>
      ) : (
        <>
          {q.data.items.map((w) => (
            <div className="warehouse-row" key={w.id}>
              <div>
                <strong>{w.name || "Unnamed warehouse"}</strong>
                <small>{w.id}</small>
                <button
                  className="link-button"
                  onClick={() => setSelected(selected === w.id ? "" : w.id)}
                >
                  {selected === w.id ? "Hide stock" : "View stock"}
                </button>
                <button
                  className="link-button"
                  onClick={() => selectWarehouse(w.id)}
                >
                  Select for mapping review
                </button>
                {selected === w.id && (
                  <StockRows
                    key={w.id}
                    scope={scope}
                    source={source}
                    account={account}
                    warehouse={w.id}
                  />
                )}
              </div>
              <div className="warehouse-meta">
                <span>
                  {source === "ozon"
                    ? w.is_rfbs === null
                      ? "Type unknown"
                      : w.is_rfbs
                        ? "rFBS"
                        : "FBS"
                    : "Source warehouse"}
                </span>
                <small>
                  {w.mapped === null
                    ? "Mapping not checked"
                    : w.mapped
                      ? "Confirmed mapping"
                      : "Unmapped"}
                </small>
                <small>
                  {w.stock_rows === 0
                    ? "No stock observations"
                    : `${w.stock_rows} stock observations`}
                </small>
              </div>
            </div>
          ))}
          <div className="pagination">
            <button
              className="secondary"
              disabled={offset === 0}
              onClick={() => {
                setOffset(Math.max(0, offset - 50));
                setSelected("");
              }}
            >
              Previous
            </button>
            <button
              className="secondary"
              disabled={!q.data.has_next}
              onClick={() => {
                setOffset(offset + 50);
                setSelected("");
              }}
            >
              Next
            </button>
          </div>
        </>
      )}
    </section>
  );
}
export function Warehouses({
  scope,
  confirmation,
}: {
  scope: Scope;
  confirmation?: Confirmation;
}) {
  const [moyWarehouse, setMoyWarehouse] = useState("");
  const [ozonWarehouse, setOzonWarehouse] = useState("");
  return (
    <>
      <div className="heading-row">
        <div>
          <p className="eyebrow">Locations</p>
          <h1>Warehouses</h1>
          <p className="subtitle">
            Source locations stay separate until a mapping is confirmed.
          </p>
        </div>
      </div>
      <Notice>
        Missing stock observations are unknown, not zero. Warehouse names do not
        establish a mapping.
      </Notice>
      <div className="warehouse-grid">
        <WarehouseList
          scope={scope}
          source="moysklad"
          account={scope.moy}
          other={scope.ozon}
          selectWarehouse={setMoyWarehouse}
        />
        <WarehouseList
          scope={scope}
          source="ozon"
          account={scope.ozon}
          other={scope.moy}
          selectWarehouse={setOzonWarehouse}
        />
      </div>
      <p>
        Selected warehouses: MoySklad {moyWarehouse || "none"}; Ozon{" "}
        {ozonWarehouse || "none"}.
      </p>
      {moyWarehouse && ozonWarehouse && (
        <WarehouseReview
          key={`${moyWarehouse}:${ozonWarehouse}`}
          scope={scope}
          moy={moyWarehouse}
          ozon={ozonWarehouse}
          confirmation={confirmation}
        />
      )}
    </>
  );
}
export function Diagnostics({
  report,
  loading,
  error,
}: {
  report?: Report;
  loading: boolean;
  error: unknown;
}) {
  if (loading) return <Loading />;
  if (error) return <Failure error={error} />;
  if (!report)
    return <Notice>Select both registered accounts to see diagnostics.</Notice>;
  return (
    <>
      <div className="heading-row">
        <div>
          <p className="eyebrow">Evidence</p>
          <h1>Diagnostics</h1>
          <p className="subtitle">
            What is known, what is missing, and what still needs review.
          </p>
        </div>
        <span className="readonly">
          <i />
          Publication disabled
        </span>
      </div>
      <div className="decision">
        <div>
          <span>Stock calculation</span>
          <strong>{text(report.decision_status)}</strong>
        </div>
        <p>
          {report.decision_status === "NOT_CHECKED"
            ? "No confirmed product and warehouse mapping pair was evaluated."
            : "Review the reasons below before relying on this result."}
        </p>
      </div>
      {report.decision_reasons.length > 0 && (
        <div className="reasons">
          {report.decision_reasons.map((r) => (
            <code key={r}>{r}</code>
          ))}
        </div>
      )}
      <div className="diagnostics">
        {report.checks.map((c) => (
          <div className="check" key={c.code}>
            <div>
              <strong>{text(c.code)}</strong>
              <p>{c.detail}</p>
              <code>{c.code}</code>
            </div>
            <State status={c.status} />
          </div>
        ))}
      </div>
    </>
  );
}

function savedWorkspace() {
  try {
    const value = JSON.parse(sessionStorage.getItem("styk-workspace") || "{}");
    return {
      tenant: typeof value.tenant === "string" ? value.tenant : "",
      moy: typeof value.moy === "string" ? value.moy : "",
      ozon: typeof value.ozon === "string" ? value.ozon : "",
    };
  } catch {
    return { tenant: "", moy: "", ozon: "" };
  }
}

export function App() {
  const client = useQueryClient();
  const confirmation = useConfirmation();
  const importSubmission = useImportSubmission();
  const navigate = useNavigate();
  useEffect(() => {
    if (!confirmation.pending && !importSubmission.pending) return;
    const warn = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = "";
    };
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [confirmation.pending, importSubmission.pending]);
  const session = useQuery({
    queryKey: ["session"],
    queryFn: ({ signal }) => api<null>("/api/session", signal),
    retry: false,
  });
  const [initialWorkspace] = useState(savedWorkspace);
  const [tenant, setTenant] = useState(initialWorkspace.tenant),
    [chosen, setChosen] = useState(initialWorkspace.tenant),
    [moy, setMoy] = useState(initialWorkspace.moy),
    [ozon, setOzon] = useState(initialWorkspace.ozon);
  useEffect(() => {
    try {
      sessionStorage.setItem(
        "styk-workspace",
        JSON.stringify({ tenant: chosen, moy, ozon }),
      );
    } catch {
      // The console remains usable when browser storage is unavailable.
    }
  }, [chosen, moy, ozon]);
  const accounts = useQuery({
    queryKey: ["accounts", chosen],
    queryFn: ({ signal }) =>
      api<{ accounts: Account[]; truncated: boolean }>(
        "/api/registrations?" + params({ tenant: chosen }),
        signal,
      ),
    enabled: session.isSuccess && !!chosen,
  });
  const scope = { tenant: chosen, moy, ozon };
  useEffect(() => {
    if (!accounts.data) return;
    if (
      moy &&
      !accounts.data.accounts.some(
        (a) => a.source === "moysklad" && a.account === moy,
      )
    )
      setMoy("");
    if (
      ozon &&
      !accounts.data.accounts.some(
        (a) => a.source === "ozon" && a.account === ozon,
      )
    )
      setOzon("");
  }, [accounts.data, moy, ozon]);
  const report = useQuery({
    queryKey: ["diagnostics", scope],
    queryFn: ({ signal }) =>
      api<Report>("/api/diagnostics?" + scopeParams(scope), signal),
    enabled: session.isSuccess && !!chosen,
  });
  if (session.isLoading)
    return (
      <main className="login">
        <Loading />
      </main>
    );
  if (!session.isSuccess)
    return (
      <>
        {confirmation.sessionLost && (
          <p role="alert">
            The previous confirmation may have committed. After signing in,
            inspect mapping history for the original account pair; the new
            session cannot replay it.
          </p>
        )}
        {importSubmission.sessionLost && (
          <p role="alert">
            The previous import request may have been queued. Sign in and
            inspect the original account's import history.
          </p>
        )}
        <Login
          onDone={() => {
            void client.invalidateQueries({ queryKey: ["session"] });
          }}
        />
      </>
    );
  async function logout() {
    await fetch("/api/session", {
      method: "DELETE",
      credentials: "same-origin",
    });
    client.clear();
    window.location.assign("/");
  }
  return (
    <div className="shell">
      <aside className="sidebar">
        <div className="brand">
          S<span>TYK</span>
          <small>Integration workspace</small>
        </div>
        <p className="nav-title">Workspace</p>
        <nav>
          <NavLink to="/" end>
            <span>◫</span>Overview
          </NavLink>
          <NavLink to="/integrations">
            <span>◈</span>Integrations
          </NavLink>
          <NavLink to="/products">
            <span>◇</span>Products
          </NavLink>
          <NavLink to="/warehouses">
            <span>▤</span>Warehouses
          </NavLink>
          <NavLink to="/diagnostics">
            <span>◉</span>Diagnostics
          </NavLink>
        </nav>
        <div className="sidebar-foot">
          <div className="sidebar-badge">Marketplace writes disabled</div>
          <p>
            Observe first.
            <br />
            No external changes are made.
          </p>
          <button
            disabled={
              (!!confirmation.pending && !confirmation.sessionLost) ||
              (!!importSubmission.pending && !importSubmission.sessionLost)
            }
            onClick={() => void logout()}
          >
            Sign out
          </button>
        </div>
      </aside>
      <main className="main">
        <header className="topbar">
          <span>
            Workspace <b>/</b> {chosen || "Choose tenant"}
          </span>
          <span className="operator">
            <i /> Local operator
          </span>
        </header>
        <div className="content">
          <ConfirmationNotice
            confirmation={confirmation}
            scope={scope}
            restore={(original) => {
              setTenant(original.tenant);
              setChosen(original.tenant);
              setMoy(original.moy);
              setOzon(original.ozon);
            }}
          />
          <ImportSubmissionNotice
            submission={importSubmission}
            scope={{
              tenant: chosen,
              provider:
                importSubmission.pending?.intent.scope.provider ??
                (importSubmission.result?.operation.provider === "moysklad"
                  ? "moysklad"
                  : "ozon"),
              account:
                (importSubmission.pending?.intent.scope.provider ??
                  importSubmission.result?.operation.provider) === "moysklad"
                  ? moy
                  : ozon,
            }}
            restore={(original) => {
              setTenant(original.tenant);
              setChosen(original.tenant);
              if (original.provider === "moysklad") setMoy(original.account);
              else setOzon(original.account);
              navigate("/integrations");
            }}
          />
          <div className="scope-panel">
            <form
              onSubmit={(e) => {
                e.preventDefault();
                setChosen(tenant.trim());
                setMoy("");
                setOzon("");
              }}
            >
              <label>
                Tenant
                <input
                  value={tenant}
                  onChange={(e) => setTenant(e.target.value)}
                  placeholder="Tenant ID"
                />
              </label>
              <button className="secondary">Open workspace</button>
            </form>
            {accounts.isLoading && <span>Loading accounts…</span>}
            {accounts.error && <Failure error={accounts.error} />}{" "}
            {accounts.data && (
              <>
                <label>
                  MoySklad
                  <select value={moy} onChange={(e) => setMoy(e.target.value)}>
                    <option value="">Select account</option>
                    {accounts.data.accounts
                      .filter((a) => a.source === "moysklad")
                      .map((a) => (
                        <option key={a.account} value={a.account}>
                          {a.account}
                        </option>
                      ))}
                  </select>
                </label>
                <label>
                  Ozon
                  <select
                    value={ozon}
                    onChange={(e) => setOzon(e.target.value)}
                  >
                    <option value="">Select account</option>
                    {accounts.data.accounts
                      .filter((a) => a.source === "ozon")
                      .map((a) => (
                        <option key={a.account} value={a.account}>
                          {a.account}
                        </option>
                      ))}
                  </select>
                </label>
                {accounts.data.accounts.length === 0 && (
                  <span>No registered accounts for this tenant.</span>
                )}
                {accounts.data.truncated && (
                  <span>Account list is truncated.</span>
                )}
              </>
            )}
          </div>
          <Routes>
            <Route
              path="/integrations"
              element={
                <Integrations
                  key={chosen}
                  tenant={chosen}
                  accounts={accounts.data?.accounts ?? []}
                  selected={{ moy, ozon }}
                  report={report.data}
                  submission={importSubmission}
                />
              }
            />
            <Route
              path="/"
              element={
                <Overview
                  report={report.data}
                  loading={report.isLoading && !!chosen}
                  error={report.error}
                />
              }
            />
            <Route
              path="/products"
              element={
                <Products
                  key={`${scope.tenant}:${scope.moy}:${scope.ozon}`}
                  scope={scope}
                  confirmation={confirmation}
                />
              }
            />
            <Route
              path="/warehouses"
              element={
                <Warehouses
                  key={`${scope.tenant}:${scope.moy}:${scope.ozon}`}
                  scope={scope}
                  confirmation={confirmation}
                />
              }
            />
            <Route
              path="/diagnostics"
              element={
                <Diagnostics
                  report={report.data}
                  loading={report.isLoading && !!chosen}
                  error={report.error}
                />
              }
            />
          </Routes>
        </div>
      </main>
    </div>
  );
}
