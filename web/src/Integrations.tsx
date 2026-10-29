import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { ImportPanel } from "./ImportPanel";
import {
  useImportSubmission,
  type ImportSubmissionState,
} from "./ImportSubmission";
import {
  api,
  post,
  params,
  ApiError,
  type Account,
  type Batch,
  type Report,
} from "./api";

type Provider = {
  provider: "moysklad" | "ozon";
  account?: string;
  status:
    | "NOT_CONFIGURED"
    | "CONFIGURED_NOT_VERIFIED"
    | "VERIFIED"
    | "VERIFICATION_FAILED";
  registration:
    "NOT_CHECKED" | "NOT_REGISTERED" | "REGISTERED" | "OWNERSHIP_CONFLICT";
  check?: { checked_at: string; identity: string; reason?: string };
  import?: Batch;
};
export function Integrations({
  tenant,
  accounts,
  selected,
  report,
  submission,
}: {
  tenant: string;
  accounts: Account[];
  selected?: { moy: string; ozon: string };
  report?: Report;
  submission?: ImportSubmissionState;
}) {
  const localSubmission = useImportSubmission();
  const imports = submission ?? localSubmission;
  const client = useQueryClient();
  const [busy, setBusy] = useState("");
  const [acks, setAcks] = useState<Record<string, boolean>>({});
  const [message, setMessage] = useState("");
  const [uncertain, setUncertain] = useState("");
  const query = useQuery({
    queryKey: ["integrations", tenant],
    queryFn: ({ signal }) =>
      api<{ providers: Provider[] }>(
        "/api/integrations?" + params({ tenant }),
        signal,
      ),
    enabled: !!tenant,
    retry: false,
  });
  async function probe(provider: string) {
    if (busy) return;
    setBusy(provider);
    setMessage("");
    try {
      await post(`/api/integrations/${provider}/probe`, {});
      await query.refetch();
    } catch (error) {
      setMessage(
        error instanceof ApiError && error.status === 401
          ? "Session expired. Sign in again."
          : "Connection check failed or was interrupted. No import was started.",
      );
      if (error instanceof ApiError && error.status === 401)
        void client.invalidateQueries({ queryKey: ["session"] });
    } finally {
      setBusy("");
    }
  }
  async function reconcile(provider: string) {
    setBusy(provider);
    try {
      const result = await query.refetch();
      if (result.isError || !result.data) throw new Error("status unavailable");
      setUncertain("");
      setMessage(
        "Registration status was checked against the database. Review it before proceeding.",
      );
      void client.invalidateQueries({ queryKey: ["accounts", tenant] });
    } catch {
      setMessage(
        "Registration status is still unknown. Check again before retrying.",
      );
    } finally {
      setBusy("");
    }
  }
  async function register(provider: string) {
    if (busy || uncertain || !acks[provider]) return;
    setBusy(provider);
    setMessage("");
    try {
      await post(`/api/integrations/${provider}/register`, {
        tenant,
        acknowledge: true,
      });
      setAcks((old) => ({ ...old, [provider]: false }));
      setMessage(
        "Registration recorded. Historical imports were not automatically included.",
      );
      await Promise.all([
        query.refetch(),
        client.invalidateQueries({ queryKey: ["accounts", tenant] }),
        client.invalidateQueries({ queryKey: ["diagnostics"] }),
      ]);
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) {
        setUncertain(provider);
        setMessage(
          "The session ended; registration may have committed. Sign in and check its status.",
        );
        void client.invalidateQueries({ queryKey: ["session"] });
      } else if (
        error instanceof ApiError &&
        error.code === "OWNERSHIP_CONFLICT"
      ) {
        setMessage(
          "This configured account is already registered to another tenant. It cannot be transferred here.",
        );
        void query.refetch();
      } else if (
        error instanceof ApiError &&
        [
          "PROBE_REQUIRED",
          "INVALID_REQUEST",
          "NOT_CONFIGURED",
          "FORBIDDEN",
        ].includes(error.code || "")
      ) {
        setMessage(
          "Registration rejected. Check the configuration, connection test and acknowledgement.",
        );
      } else {
        setUncertain(provider);
        setMessage(
          "Registration outcome is unknown. Check the exact registration before another attempt.",
        );
      }
    } finally {
      setBusy("");
    }
  }
  if (!tenant)
    return (
      <div className="notice">Select a tenant to inspect its integrations.</div>
    );
  return (
    <>
      <div className="heading-row">
        <div>
          <p className="eyebrow">Local operator</p>
          <h1>Integrations</h1>
          <p className="subtitle">
            Inspect configured providers and register an account explicitly.
          </p>
        </div>
      </div>
      <div className="notice">
        A connection check is a bounded read, not proof of ownership, complete
        permissions or synchronization readiness. Registrations are trusted
        local operator attestations, not production tenant authorization.
      </div>
      {message && <p role="alert">{message}</p>}
      {uncertain && (
        <button disabled={!!busy} onClick={() => void reconcile(uncertain)}>
          Check registration outcome
        </button>
      )}
      {query.isLoading && <p role="status">Loading integrations…</p>}
      {query.error && (
        <p role="alert">Integration status unavailable. Check the local API.</p>
      )}
      <div className="warehouse-grid">
        {query.data?.providers.map((p) => {
          const registeredAccounts = accounts.filter(
            (a) => a.source === p.provider,
          );
          const selectedBatch = (
            p.provider === "moysklad" ? selected?.moy : selected?.ozon
          )
            ? report?.batches[p.provider]
            : undefined;
          const checkAge = p.check
            ? Date.now() - Date.parse(p.check.checked_at)
            : NaN;
          const recentProbe =
            p.status === "VERIFIED" &&
            Number.isFinite(checkAge) &&
            checkAge >= 0 &&
            checkAge <= 5 * 60 * 1000;
          return (
            <article className="source-card" key={p.provider}>
              <div className="source-top">
                <h2>{p.provider === "moysklad" ? "MoySklad" : "Ozon"}</h2>
                <strong>{p.status}</strong>
              </div>
              <p>Configured account: {p.registration}</p>
              {p.account && <p>Configured account ID: {p.account}</p>}
              <p>
                Previously registered accounts:{" "}
                {registeredAccounts.length || "none"}
              </p>
              {p.status === "VERIFIED" && !recentProbe && (
                <p>
                  Connection test is older than five minutes; test again before
                  registration.
                </p>
              )}
              {registeredAccounts.length > 0 && (
                <ul>
                  {registeredAccounts.map((account) => (
                    <li key={account.account}>
                      {account.account} · registered{" "}
                      <time dateTime={account.registered_at}>
                        {new Date(account.registered_at).toLocaleString()}
                      </time>
                    </li>
                  ))}
                </ul>
              )}
              <p>
                {p.check ? (
                  <>
                    Last check:{" "}
                    <time dateTime={p.check.checked_at}>
                      {new Date(p.check.checked_at).toLocaleString()}
                    </time>{" "}
                    · {p.check.reason || "read succeeded"}. Identity:{" "}
                    {p.check.identity === "MATCHED_RETURNED_PRODUCT"
                      ? "a returned product matched the configured account"
                      : "not independently established"}
                    .
                  </>
                ) : (
                  "Live connection not checked in this session."
                )}
              </p>
              <button
                disabled={!!busy || p.status === "NOT_CONFIGURED"}
                onClick={() => void probe(p.provider)}
              >
                {busy === p.provider ? "Working…" : "Test connection"}
              </button>
              <p>
                {p.import?.present ? (
                  <>
                    Latest{" "}
                    {p.import.completed
                      ? "completed import"
                      : "incomplete import"}
                    :{" "}
                    {p.import.observed_at && (
                      <time dateTime={p.import.observed_at}>
                        {new Date(p.import.observed_at).toLocaleString()}
                      </time>
                    )}{" "}
                    · age {p.import.age}.{" "}
                    {p.import.observed_at &&
                      Date.now() - Date.parse(p.import.observed_at) > 3600000 &&
                      "Stale observation."}{" "}
                    Products {p.import.counts?.products ?? "unknown"};
                    warehouses {p.import.counts?.warehouses ?? "unknown"}; stock
                    rows {p.import.counts?.stock_rows ?? "unknown"}.
                  </>
                ) : (
                  "No eligible import available."
                )}
              </p>
              {!p.import?.present && selectedBatch?.present && (
                <p>
                  Selected registered account:{" "}
                  {selectedBatch.completed
                    ? "completed import"
                    : "incomplete import"}
                  ;{" "}
                  {selectedBatch.observed_at && (
                    <time dateTime={selectedBatch.observed_at}>
                      {new Date(selectedBatch.observed_at).toLocaleString()}
                    </time>
                  )}
                  ; age {selectedBatch.age}; products{" "}
                  {selectedBatch.counts?.products ?? "unknown"}, warehouses{" "}
                  {selectedBatch.counts?.warehouses ?? "unknown"}, stock rows{" "}
                  {selectedBatch.counts?.stock_rows ?? "unknown"}.
                </p>
              )}
              <p>
                Completed traversal is not a point-in-time snapshot; missing
                stock rows are unknown.
              </p>
              {p.registration === "NOT_REGISTERED" && (
                <div>
                  <p>
                    Register the account configured in the local process to
                    tenant {tenant}. Confirm its identity using your local
                    configuration. A successful test within five minutes is
                    required. Historical imports are excluded.
                  </p>
                  <label>
                    <input
                      type="checkbox"
                      checked={!!acks[p.provider]}
                      onChange={(e) =>
                        setAcks({ ...acks, [p.provider]: e.target.checked })
                      }
                    />{" "}
                    I attest this configured account belongs to this tenant.
                  </label>
                  <p>
                    This is an operator attestation, not independent ownership
                    verification.
                  </p>
                  <button
                    disabled={
                      !!busy || !!uncertain || !acks[p.provider] || !recentProbe
                    }
                    onClick={() => void register(p.provider)}
                  >
                    Register account
                  </button>
                </div>
              )}
              {p.registration === "OWNERSHIP_CONFLICT" && (
                <p role="alert">
                  Account is already assigned to another tenant; reassignment is
                  unavailable.
                </p>
              )}
              <nav aria-label={`${p.provider} navigation`}>
                <Link to="/products">Products</Link>
                {" · "}
                <Link to="/warehouses">Warehouses</Link>
                {" · "}
                <Link to="/diagnostics">Diagnostics</Link>
              </nav>
            </article>
          );
        })}
      </div>
      {selected?.ozon &&
        accounts.some(
          (account) =>
            account.source === "ozon" && account.account === selected.ozon,
        ) && (
          <ImportPanel
            key={`${tenant}:${selected.ozon}`}
            scope={{ tenant, account: selected.ozon }}
            submission={imports}
            configured={
              !!query.data?.providers.some(
                (provider) =>
                  provider.provider === "ozon" &&
                  provider.account === selected.ozon &&
                  provider.status !== "NOT_CONFIGURED",
              )
            }
          />
        )}
    </>
  );
}
