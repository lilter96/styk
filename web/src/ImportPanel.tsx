import { useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import {
  api,
  importProviders,
  importAccount,
  sameImportScope,
  post,
  params,
  ApiError,
  type ImportOperation,
  type Batch,
  type ImportScope,
} from "./api";
import type { ImportSubmissionState } from "./ImportSubmission";

const phaseLabels: Record<string, string> = {
  READING_PRODUCTS: "Products",
  READING_VARIANTS: "Variants",
  READING_WAREHOUSES: "Warehouses",
  READING_STOCK: "FBS stock",
  READING_AGGREGATE_STOCK: "Aggregate stock",
  READING_WAREHOUSE_STOCK: "Warehouse stock",
  PERSISTING: "Save batch",
};
type History = { items: ImportOperation[]; has_next: boolean; offset: number };
const active = (state: string) =>
  ["QUEUED", "RUNNING", "OUTCOME_UNKNOWN"].includes(state);
const scopeQuery = (scope: ImportScope) =>
  params({ tenant: scope.tenant, ...importAccount(scope) });
const poll = (query: { state: { data?: ImportOperation; error: unknown } }) =>
  !query.state.error && query.state.data && active(query.state.data.state)
    ? 2000
    : false;
const pollHistory = (query: { state: { data?: History; error: unknown } }) =>
  !query.state.error && query.state.data?.items.some((op) => active(op.state))
    ? 2000
    : false;
const descriptions: Record<string, string> = {
  QUEUED: "Waiting for an available worker.",
  RUNNING: "Import in progress.",
  SUCCEEDED: "Catalog traversal completed and a batch is saved.",
  FAILED: "Import failed. Previous completed imports remain available.",
  CANCELLED: "Import was cancelled before execution.",
  OUTCOME_UNKNOWN: "The final database outcome requires reconciliation.",
};
const failures: Record<string, string> = {
  LIMIT_REACHED: "An execution limit stopped the traversal.",
  API_FAILURE: "The source request failed or timed out.",
  INVALID_RESPONSE: "The source response could not be validated.",
  PERSISTENCE_FAILURE: "The batch could not be saved atomically.",
  CONFIGURATION_UNAVAILABLE:
    "The matching worker configuration is unavailable.",
  LEASE_EXHAUSTED: "The worker lease expired on the final attempt.",
  CANCELLED: "The attempt stopped before completion.",
};
function timestamp(value: string | null) {
  return value ? new Date(value).toLocaleString() : "—";
}
function Confirm({
  title,
  children,
  onClose,
}: {
  title: string;
  children: React.ReactNode;
  onClose: () => void;
}) {
  const dialog = useRef<HTMLDivElement>(null);
  useEffect(() => {
    dialog.current?.querySelector<HTMLInputElement>("input,button")?.focus();
  }, []);
  return (
    <div
      ref={dialog}
      className="import-confirm"
      role="dialog"
      aria-label={title}
      onKeyDown={(event) => {
        if (event.key === "Escape") onClose();
      }}
    >
      <h3>{title}</h3>
      {children}
      <button className="secondary" onClick={onClose}>
        Close
      </button>
    </div>
  );
}
export function ImportPanel({
  scope,
  configured,
  submission,
  latestBatch,
}: {
  scope: ImportScope;
  configured: boolean;
  submission: ImportSubmissionState;
  latestBatch?: Batch;
}) {
  const client = useQueryClient();
  const provider = importProviders[scope.provider];
  const isMoy = scope.provider === "moysklad";
  const [offset, setOffset] = useState(0),
    [selected, setSelected] = useState(""),
    [confirm, setConfirm] = useState(false),
    [ack, setAck] = useState(false),
    [cancelConfirm, setCancelConfirm] = useState(false),
    [cancelBusy, setCancelBusy] = useState(false),
    [inspectionBusy, setInspectionBusy] = useState(false),
    [submitError, setSubmitError] = useState(""),
    [cancelMessage, setCancelMessage] = useState("");
  const cancelling = useRef(false),
    invalidated = useRef(new Set<string>());
  const queryString = scopeQuery(scope);
  const latest = useQuery({
    queryKey: [
      "import-history",
      scope.tenant,
      scope.provider,
      scope.account,
      0,
    ],
    queryFn: ({ signal }) =>
      api<History>(`/api/imports?${queryString}&limit=10&offset=0`, signal),
    retry: false,
    refetchInterval: pollHistory,
    refetchIntervalInBackground: false,
    refetchOnWindowFocus: false,
  });
  const older = useQuery({
    queryKey: [
      "import-history",
      scope.tenant,
      scope.provider,
      scope.account,
      offset,
    ],
    queryFn: ({ signal }) =>
      api<History>(
        `/api/imports?${queryString}&limit=10&offset=${offset}`,
        signal,
      ),
    enabled: offset > 0,
    retry: false,
    refetchInterval: pollHistory,
    refetchIntervalInBackground: false,
    refetchOnWindowFocus: false,
  });
  const history = offset === 0 ? latest : older;
  const latestID = latest.data?.items[0]?.id || "";
  const operationID = selected || latestID;
  const detail = useQuery({
    queryKey: [
      "import-detail",
      scope.tenant,
      scope.provider,
      scope.account,
      operationID,
    ],
    queryFn: ({ signal }) =>
      api<ImportOperation>(
        `/api/imports/${operationID}?${queryString}`,
        signal,
      ),
    enabled: !!operationID,
    retry: false,
    refetchInterval: poll,
    refetchIntervalInBackground: false,
    refetchOnWindowFocus: false,
  });
  useEffect(() => {
    const r = submission.result?.operation;
    if (
      r &&
      r.provider === scope.provider &&
      r.tenant === scope.tenant &&
      r.account === scope.account
    ) {
      setOffset(0);
      setSelected(r.id);
    }
  }, [submission.result, scope.tenant, scope.provider, scope.account]);
  useEffect(() => {
    for (const error of [latest.error, history.error, detail.error])
      if (error instanceof ApiError && error.status === 401) {
        submission.markSessionLost();
        break;
      }
    // markSessionLost is invoked only on query errors; the hook object changes on render.
  }, [latest.error, history.error, detail.error]);
  useEffect(() => {
    const op = detail.data;
    if (
      op?.state !== "SUCCEEDED" ||
      op.batch_id === null ||
      invalidated.current.has(op.id)
    )
      return;
    invalidated.current.add(op.id);
    for (const key of [
      "integrations",
      "diagnostics",
      "products",
      "warehouses",
      "stock",
      "import-history",
    ])
      void client.invalidateQueries({ queryKey: [key] });
  }, [detail.data, client]);
  const current = latest.data?.items[0];
  const submitted = submission.result?.operation;
  const submittedStillActive =
    submitted?.provider === scope.provider &&
    submitted?.tenant === scope.tenant &&
    submitted.account === scope.account &&
    active(submitted.state) &&
    !(current?.id === submitted.id && !active(current.state)) &&
    !(detail.data?.id === submitted.id && !active(detail.data.state));
  const unsafe =
    !!submission.pending ||
    !!latest.error ||
    !latest.data ||
    !!submittedStillActive ||
    !!(current && active(current.state));
  const samePending =
    !!submission.pending &&
    sameImportScope(submission.pending.intent.scope, scope);
  async function cancelQueued() {
    if (cancelling.current || !detail.data || detail.data.state !== "QUEUED")
      return;
    cancelling.current = true;
    setCancelBusy(true);
    setCancelMessage("");
    try {
      await post(
        `/api/imports/${detail.data.id}/cancel`,
        {
          tenant: scope.tenant,
          ...importAccount(scope),
          acknowledge: true,
        },
        AbortSignal.timeout(30000),
      );
      setCancelConfirm(false);
      setCancelMessage("Cancellation was recorded.");
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) {
        submission.markSessionLost();
        setCancelMessage(
          "Session ended. Check the operation after signing in.",
        );
      } else if (
        error instanceof ApiError &&
        error.code === "CANCELLATION_UNAVAILABLE"
      )
        setCancelMessage(
          "The operation has started or completed; running imports cannot be cancelled.",
        );
      else
        setCancelMessage(
          "Cancellation outcome is unknown. Refresh the operation before taking further action.",
        );
    } finally {
      cancelling.current = false;
      setCancelBusy(false);
      void detail.refetch();
      void latest.refetch();
      if (offset > 0) void older.refetch();
    }
  }
  return (
    <section
      className="import-panel"
      data-provider={scope.provider}
      aria-label={`${provider.name} catalog imports`}
    >
      <div className="import-panel-head">
        <div>
          <h2>{provider.name} catalog imports</h2>
          <p className="import-scope">
            Workspace <strong>{scope.tenant}</strong>{" "}
            <span>
              Account <code>{scope.account}</code>
            </span>
          </p>
        </div>
        <button
          disabled={!configured || unsafe}
          onClick={() => {
            setAck(false);
            setSubmitError("");
            setConfirm(true);
          }}
        >
          Import catalog
        </button>
      </div>
      {!configured && (
        <p>
          The selected registered account does not match the API's configured
          {provider.name} account. History remains available.
        </p>
      )}
      {submitError && <p role="alert">{submitError}</p>}
      {latestBatch?.present && (
        <div className="import-baseline">
          <strong>
            {latestBatch.completed
              ? "Latest completed import"
              : "Incomplete source evidence"}
          </strong>
          {latestBatch.observed_at && (
            <time dateTime={latestBatch.observed_at}>
              {timestamp(latestBatch.observed_at)}
            </time>
          )}
          <p>
            Products {latestBatch.counts?.products ?? "unknown"};{" "}
            {isMoy && (
              <>variants {latestBatch.counts?.variants ?? "unknown"}; </>
            )}
            warehouses {latestBatch.counts?.warehouses ?? "unknown"}; warehouse
            stock rows {latestBatch.counts?.stock_rows ?? "unknown"}.
          </p>
        </div>
      )}

      <details className="import-help">
        <summary>What this import does</summary>
        <p>
          {provider.description} Completed traversal is not an atomic source
          snapshot. Imports do not confirm mappings or make stock safe to
          publish.
        </p>
        <p>
          If a submission response was lost and the page or session ended,
          inspect this account's import history before requesting another
          import.
        </p>
      </details>
      <p className="import-recovery">
        Lost a request response? Inspect this account's history below before
        submitting again.
      </p>
      {latest.isLoading && <p role="status">Loading import history…</p>}
      {!!latest.error && (
        <div role="alert">
          Import history is unavailable.{" "}
          <button className="secondary" onClick={() => void latest.refetch()}>
            Retry history
          </button>
        </div>
      )}
      {latest.data && !current && <p>No import operations for this account.</p>}
      {current && (
        <p>
          Latest operation:{" "}
          <strong
            className={`import-state state-${current.state.toLowerCase()}`}
          >
            {current.state}
          </strong>{" "}
          · {current.id}
        </p>
      )}
      {confirm && (
        <Confirm
          title={`Confirm ${provider.name} catalog import`}
          onClose={() => setConfirm(false)}
        >
          <p>{provider.description}</p>
          <p>A completed traversal is not an atomic source snapshot.</p>
          <p>
            Tenant {scope.tenant} · account {scope.account}. Default bounded
            limits apply.
          </p>
          <label>
            <input
              type="checkbox"
              checked={ack}
              onChange={(e) => setAck(e.target.checked)}
            />{" "}
            I explicitly request this read-only import.
          </label>{" "}
          <button
            disabled={!ack || unsafe}
            onClick={() => {
              try {
                submission.start(scope);
                setConfirm(false);
              } catch {
                setSubmitError(
                  "Secure request creation is unavailable in this browser.",
                );
              }
            }}
          >
            Confirm import
          </button>{" "}
        </Confirm>
      )}
      {samePending &&
        (submission.sessionLost ||
          submission.message.startsWith("IDEMPOTENCY_CONFLICT")) && (
          <button
            className="secondary"
            disabled={inspectionBusy}
            onClick={async () => {
              setInspectionBusy(true);
              const refreshed = await latest.refetch();
              setInspectionBusy(false);
              if (!refreshed.error && refreshed.data) {
                setOffset(0);
                submission.inspected(scope);
              }
            }}
          >
            {inspectionBusy
              ? "Refreshing history…"
              : "Refresh and inspect history"}
          </button>
        )}
      <h3>Import history</h3>
      {history.isLoading && <p role="status">Loading history…</p>}
      {!!history.error && (
        <div role="alert">
          Could not load history.{" "}
          <button className="secondary" onClick={() => void history.refetch()}>
            Retry history page
          </button>
        </div>
      )}
      {history.data?.items.length === 0 && <p>No operations on this page.</p>}
      {history.data && history.data.items.length > 0 && (
        <div
          className="import-history"
          role="region"
          aria-label="Import history list"
          tabIndex={0}
        >
          <table>
            <thead>
              <tr>
                <th>Operation</th>
                <th>State</th>
                <th>Created</th>
                <th>Started</th>
                <th>Finished</th>
                <th>Outcome</th>
                <th>Batch</th>
              </tr>
            </thead>
            <tbody>
              {history.data.items.map((op) => (
                <tr
                  key={op.id}
                  className={
                    op.id === operationID ? "import-selected" : undefined
                  }
                >
                  <td>
                    <button
                      className="import-id"
                      aria-label={`Inspect operation ${op.id}`}
                      aria-pressed={op.id === operationID}
                      onClick={() => {
                        setSelected(op.id);
                        setCancelConfirm(false);
                        setCancelMessage("");
                      }}
                    >
                      <code>{op.id}</code>
                    </button>
                    <br />
                    <small>{provider.name}</small>
                  </td>
                  <td>
                    <span
                      className={`import-state state-${op.state.toLowerCase()}`}
                    >
                      {op.state}
                    </span>
                  </td>
                  <td>{timestamp(op.created_at)}</td>
                  <td>{timestamp(op.started_at)}</td>
                  <td>{timestamp(op.finished_at)}</td>
                  <td>{op.outcome || "—"}</td>
                  <td>{op.batch_id ?? "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <div className="pagination">
        <button
          className="secondary"
          disabled={offset === 0}
          onClick={() => setOffset(Math.max(0, offset - 10))}
        >
          Previous imports
        </button>
        <button
          className="secondary"
          disabled={!history.data?.has_next || offset >= 10000}
          onClick={() => setOffset(offset + 10)}
        >
          Next imports
        </button>
      </div>
      {offset >= 10000 && history.data?.has_next && (
        <p>The history API cannot page beyond this offset.</p>
      )}
      {operationID && (
        <section
          className="import-detail"
          aria-label="Import operation details"
        >
          {detail.isLoading && <p role="status">Loading operation…</p>}
          {!!detail.error && (
            <div role="alert">
              Operation status is unavailable.{" "}
              <button
                className="secondary"
                onClick={() => void detail.refetch()}
              >
                Retry operation
              </button>
            </div>
          )}
          {detail.data && (
            <>
              <h3>
                Operation <code>{detail.data.id}</code>
              </h3>
              <p>
                <strong
                  className={`import-state state-${detail.data.state.toLowerCase()}`}
                >
                  {detail.data.state}
                </strong>{" "}
                · {descriptions[detail.data.state] || "Status unavailable."}
              </p>
              <ol className="import-phases" aria-label="Import stages">
                {provider.phases.map((phase) => (
                  <li
                    key={phase}
                    aria-current={
                      detail.data!.phase === phase ? "step" : undefined
                    }
                  >
                    {phaseLabels[phase]}
                  </li>
                ))}
              </ol>
              <p className="import-attempt">
                Phase: {detail.data.phase} · attempt {detail.data.attempt}/
                {detail.data.max_attempts} · elapsed{" "}
                {(detail.data.elapsed_ms / 1000).toFixed(1)}s.
              </p>
              <dl className="import-metrics">
                <div>
                  <dt>Logical requests</dt>
                  <dd>{detail.data.counts.requests}</dd>
                </div>
                <div>
                  <dt>HTTP attempts</dt>
                  <dd>{detail.data.counts.attempts}</dd>
                </div>
                <div>
                  <dt>Validated pages</dt>
                  <dd>{detail.data.counts.completed_pages}</dd>
                </div>
                <div>
                  <dt>Elapsed</dt>
                  <dd>
                    {(detail.data.elapsed_ms / 1000).toFixed(1)}
                    <small> sec</small>
                  </dd>
                </div>
              </dl>
              <p className="import-records">
                {isMoy ? (
                  <>
                    Products observed {detail.data.counts.products}; variants
                    observed {detail.data.counts.variants ?? "not reported"};
                    warehouses observed {detail.data.counts.warehouses};
                    aggregate stock observations{" "}
                    {detail.data.counts.observations}; warehouse stock
                    observations{" "}
                    {detail.data.counts.warehouse_observations ??
                      "not reported"}
                    .
                  </>
                ) : (
                  <>
                    Products observed {detail.data.counts.products}; warehouses
                    observed {detail.data.counts.warehouses}; stock observations{" "}
                    {detail.data.counts.observations}.
                  </>
                )}
              </p>
              <p>
                Observed records are not committed inventory data until the
                import succeeds. These counters are attempt-local. A reclaimed
                attempt resets these counters.
              </p>
              <details className="import-limits">
                <summary>Execution limits</summary>
                <p>
                  Page attempts {detail.data.counts.pages}; successful JSON
                  responses {detail.data.counts.completed_requests}.
                </p>
                <p>
                  Requests {detail.data.limits.requests}; HTTP attempts{" "}
                  {detail.data.limits.attempts}; pages{" "}
                  {detail.data.limits.pages}; products{" "}
                  {detail.data.limits.products}; warehouses{" "}
                  {detail.data.limits.warehouses}; stock observations{" "}
                  {detail.data.limits.observations};{" "}
                  {isMoy && (
                    <>
                      variants {detail.data.limits.variants ?? "not reported"};
                      warehouse stock observations{" "}
                      {detail.data.limits.warehouse_observations ??
                        "not reported"}
                      ;{" "}
                    </>
                  )}
                  response bytes {detail.data.limits.response_bytes}; duration{" "}
                  {detail.data.limits.duration_ns / 1e9}s; database transaction{" "}
                  {detail.data.limits.database_timeout_ns / 1e9}s.
                </p>
              </details>
              {detail.data.outcome && (
                <p>
                  Outcome: <code>{detail.data.outcome}</code>
                  {detail.data.limiting_resource && (
                    <> · limit: {detail.data.limiting_resource}</>
                  )}{" "}
                  {failures[detail.data.outcome] || ""}
                </p>
              )}
              {detail.data.state === "FAILED" && (
                <p>
                  You may explicitly request a new import after reviewing the
                  failure.
                </p>
              )}
              {detail.data.state === "SUCCEEDED" &&
                detail.data.batch_id !== null && (
                  <div className="import-batch">
                    <p>
                      Completed batch: <strong>{detail.data.batch_id}</strong>
                      {detail.data.batch_created === false
                        ? " (existing identical eligible batch reused; observation timestamps unchanged)"
                        : detail.data.batch_created === true
                          ? " (new batch created)"
                          : ""}
                      . Observed counts describe this traversal.{" "}
                      <Link to="/products">View latest products</Link>
                      {" · "}
                      <Link to="/warehouses">View latest warehouses</Link>.
                      These screens show the latest eligible batch, which may
                      have changed.
                    </p>
                    <Link to="/diagnostics">View diagnostics</Link>
                  </div>
                )}
              {detail.data.state === "QUEUED" && (
                <button
                  disabled={cancelBusy}
                  onClick={() => setCancelConfirm(true)}
                >
                  Cancel queued import
                </button>
              )}
              {cancelMessage && <p role="alert">{cancelMessage}</p>}
              {cancelConfirm && (
                <Confirm
                  title="Cancel queued import"
                  onClose={() => setCancelConfirm(false)}
                >
                  <p>
                    Only an import that has not started can be cancelled. This
                    does not undo a committed batch.
                  </p>
                  <button
                    disabled={cancelBusy || detail.data.state !== "QUEUED"}
                    onClick={() => void cancelQueued()}
                  >
                    {cancelBusy ? "Cancelling…" : "Confirm cancellation"}
                  </button>{" "}
                </Confirm>
              )}
            </>
          )}
        </section>
      )}
    </section>
  );
}
