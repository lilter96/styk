import { useState } from "react";
import { afterEach, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { ImportPanel } from "./ImportPanel";
import {
  ImportSubmissionNotice,
  useImportSubmission,
} from "./ImportSubmission";
import type { ImportOperation, ImportScope } from "./api";
const scope = { tenant: "tenant-a", account: "ozon-a" };
const base: ImportOperation = {
  id: "a".repeat(32),
  tenant: scope.tenant,
  account: scope.account,
  provider: "ozon",
  operation_type: "OZON_CATALOG_IMPORT",
  state: "QUEUED",
  phase: "QUEUED",
  outcome: null,
  limiting_resource: null,
  created_at: "2026-01-01T00:00:00Z",
  started_at: null,
  finished_at: null,
  attempt_started_at: null,
  progress_at: null,
  lease_expires_at: null,
  attempt: 0,
  max_attempts: 3,
  elapsed_ms: 0,
  batch_id: null,
  batch_created: null,
  counts: {
    requests: 0,
    attempts: 0,
    pages: 0,
    completed_requests: 0,
    completed_pages: 0,
    products: 0,
    warehouses: 0,
    observations: 0,
  },
  limits: {
    requests: 100,
    attempts: 100,
    pages: 100,
    products: 1000,
    warehouses: 1000,
    observations: 10000,
    response_bytes: 4194304,
    duration_ns: 180000000000,
    database_timeout_ns: 10000000000,
  },
};
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});
function Harness({ client }: { client: QueryClient }) {
  const [current, setCurrent] = useState<ImportScope>(scope);
  const submission = useImportSubmission();
  return (
    <>
      <button
        onClick={() => setCurrent({ tenant: "tenant-b", account: "ozon-b" })}
      >
        Switch scope
      </button>
      <ImportSubmissionNotice
        submission={submission}
        scope={current}
        restore={setCurrent}
      />
      <ImportPanel
        key={`${current.tenant}:${current.account}`}
        scope={current}
        submission={submission}
        configured={true}
      />
      <button
        onClick={() =>
          void client.invalidateQueries({ queryKey: ["import-history"] })
        }
      >
        Refresh test data
      </button>
    </>
  );
}
function setup(
  opts: {
    initial?: ImportOperation;
    lost?: boolean;
    unauthorized?: boolean;
    conflict?: boolean;
    cancelConflict?: boolean;
    pages?: boolean;
  } = {},
) {
  let operation = opts.initial,
    attempts = 0,
    historyUnavailable = false;
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const invalidate = vi.spyOn(client, "invalidateQueries");
  const fetcher = vi
    .spyOn(globalThis, "fetch")
    .mockImplementation(async (input, init) => {
      const path = String(input);
      if (init?.method === "POST" && path === "/api/imports") {
        attempts++;
        if (opts.unauthorized)
          return new Response('{"error":"UNAUTHORIZED"}', { status: 401 });
        if (opts.conflict)
          return new Response('{"error":"IDEMPOTENCY_CONFLICT"}', {
            status: 409,
          });
        operation = { ...base };
        if (opts.lost && attempts === 1) throw new TypeError("connection lost");
        return new Response(
          JSON.stringify({ operation, idempotent_replay: !!opts.lost }),
          { status: attempts === 1 ? 201 : 200 },
        );
      }
      if (init?.method === "POST" && path.endsWith("/cancel")) {
        if (opts.cancelConflict) {
          operation = { ...base, state: "RUNNING", phase: "READING_PRODUCTS" };
          return new Response('{"error":"CANCELLATION_UNAVAILABLE"}', {
            status: 409,
          });
        }
        operation = {
          ...base,
          state: "CANCELLED",
          phase: "CANCELLED",
          outcome: "CANCELLED",
        };
        return new Response(null, { status: 204 });
      }
      if (path.startsWith("/api/imports?")) {
        if (historyUnavailable)
          return new Response('{"error":"IMPORT_PERSISTENCE_UNKNOWN"}', {
            status: 503,
          });
        const query = new URLSearchParams(path.split("?")[1]);
        if (query.get("tenant") !== scope.tenant)
          return new Response('{"items":[],"has_next":false,"offset":0}', {
            status: 200,
          });
        if (opts.pages && query.get("offset") === "10")
          return new Response(
            JSON.stringify({
              items: [
                {
                  ...base,
                  id: "b".repeat(32),
                  state: "FAILED",
                  outcome: "API_FAILURE",
                },
              ],
              has_next: false,
              offset: 10,
            }),
            { status: 200 },
          );
        return new Response(
          JSON.stringify({
            items: operation ? [operation] : [],
            has_next: !!opts.pages,
            offset: 0,
          }),
          { status: 200 },
        );
      }
      if (path.startsWith("/api/imports/"))
        return new Response(JSON.stringify(operation || base), { status: 200 });
      throw new Error("unexpected request");
    });
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <Harness client={client} />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return {
    fetcher,
    client,
    invalidate,
    setOperation: (value: ImportOperation) => {
      operation = value;
    },
    setHistoryUnavailable: (value: boolean) => {
      historyUnavailable = value;
    },
  };
}
async function openConfirm() {
  fireEvent.click(
    await screen.findByRole("button", { name: "Import catalog" }),
  );
  await screen.findByRole("dialog", { name: "Confirm Ozon catalog import" });
}
it("requires acknowledgement and safely retries one immutable intent after switching accounts", async () => {
  const { fetcher } = setup({ lost: true });
  await screen.findByText(/No import operations/);
  expect(
    fetcher.mock.calls.filter(([, init]) => init?.method === "POST"),
  ).toHaveLength(0);
  await openConfirm();
  const button = screen.getByRole("button", {
    name: "Confirm import",
  }) as HTMLButtonElement;
  expect(button.disabled).toBe(true);
  fireEvent.click(screen.getByLabelText(/I explicitly request/));
  fireEvent.click(button);
  fireEvent.click(button);
  await screen.findByText(/Submission outcome unknown/);
  fireEvent.click(screen.getByText("Switch scope"));
  expect(
    screen.queryByRole("button", { name: "Retry same import request" }),
  ).toBeNull();
  fireEvent.click(screen.getByText("Return to original import scope"));
  fireEvent.click(
    screen.getByRole("button", { name: "Retry same import request" }),
  );
  await screen.findByText(/existing import operation was recovered/);
  const calls = fetcher.mock.calls.filter(
    ([p, init]) => p === "/api/imports" && init?.method === "POST",
  );
  expect(calls).toHaveLength(2);
  expect(calls[0][1]?.body).toBe(calls[1][1]?.body);
  const payload = JSON.parse(String(calls[0][1]?.body));
  expect(payload.tenant).toBe(scope.tenant);
  expect(payload.ozon_account).toBe(scope.account);
  expect(payload.idempotency_key).toMatch(/^[0-9a-f-]{36}$/);
  expect(payload.limits).toBeUndefined();
});
it("reports when secure request identity cannot be generated", async () => {
  vi.spyOn(crypto, "randomUUID").mockImplementation(() => {
    throw new Error("unavailable");
  });
  const { fetcher } = setup();
  await openConfirm();
  fireEvent.click(screen.getByLabelText(/I explicitly request/));
  fireEvent.click(screen.getByRole("button", { name: "Confirm import" }));
  await screen.findByText(/Secure request creation is unavailable/);
  expect(
    fetcher.mock.calls.filter(([, init]) => init?.method === "POST"),
  ).toHaveLength(0);
});
it("renders real progress, resets attempt-local counts, and refreshes queries after success", async () => {
  const running = {
    ...base,
    state: "RUNNING" as const,
    phase: "READING_STOCK",
    attempt: 1,
    counts: { ...base.counts, products: 8, completed_pages: 3 },
  };
  const { client, invalidate } = setup({ initial: running });
  await screen.findByText(/Products observed 8/);
  const key = ["import-detail", scope.tenant, scope.account, base.id];
  client.setQueryData(key, {
    ...running,
    attempt: 2,
    counts: { ...running.counts, products: 2 },
  });
  await screen.findByText(/Products observed 2/);
  expect(screen.getByText(/attempt 2\/3/)).toBeTruthy();
  client.setQueryData(key, {
    ...running,
    state: "SUCCEEDED",
    phase: "COMPLETED",
    outcome: "COMPLETE_TRAVERSAL",
    batch_id: 42,
    batch_created: true,
  });
  await screen.findByText(/Completed batch:/);
  await waitFor(() =>
    expect(
      invalidate.mock.calls.some(([arg]) =>
        JSON.stringify(arg).includes('"products"'),
      ),
    ).toBe(true),
  );
  for (const expected of ["integrations", "diagnostics", "warehouses"])
    expect(
      invalidate.mock.calls.some(([arg]) =>
        JSON.stringify(arg).includes(`"${expected}"`),
      ),
    ).toBe(true);
});
it("does not offer another import while a newly queued result is newer than cached history", async () => {
  const { client } = setup();
  await screen.findByText(/No import operations/);
  await openConfirm();
  fireEvent.click(screen.getByLabelText(/I explicitly request/));
  fireEvent.click(screen.getByRole("button", { name: "Confirm import" }));
  await screen.findByText("Import request accepted.");
  client.setQueryData(["import-history", scope.tenant, scope.account, 0], {
    items: [],
    has_next: false,
    offset: 0,
  });
  await waitFor(() =>
    expect(
      (
        screen.getByRole("button", {
          name: "Import catalog",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true),
  );
});
for (const [state, outcome, description] of [
  ["FAILED", "LIMIT_REACHED", "stopped the traversal"],
  ["CANCELLED", "CANCELLED", "cancelled before"],
  ["OUTCOME_UNKNOWN", null, "requires reconciliation"],
] as const)
  it(`displays ${state} without inventing a result`, async () => {
    setup({
      initial: {
        ...base,
        state,
        outcome,
        limiting_resource: outcome === "LIMIT_REACHED" ? "pages" : null,
      },
    });
    await screen.findByText(new RegExp(description, "i"));
    if (state === "OUTCOME_UNKNOWN")
      expect(
        (
          screen.getByRole("button", {
            name: "Import catalog",
          }) as HTMLButtonElement
        ).disabled,
      ).toBe(true);
    expect(screen.queryByText(/Completed batch:/)).toBeNull();
  });
it("cancels only a queued operation after confirmation", async () => {
  const { fetcher } = setup({ initial: base });
  await screen.findByText(/Waiting for an available worker/);
  fireEvent.click(screen.getByRole("button", { name: "Cancel queued import" }));
  await screen.findByRole("dialog", { name: "Cancel queued import" });
  fireEvent.click(screen.getByRole("button", { name: "Confirm cancellation" }));
  await screen.findByText(/Cancellation was recorded/);
  expect(
    fetcher.mock.calls.filter(([p]) => String(p).endsWith("/cancel")),
  ).toHaveLength(1);
});
it("reports a cancellation race and does not claim success", async () => {
  setup({ initial: base, cancelConflict: true });
  await screen.findByText(/Waiting for an available worker/);
  fireEvent.click(screen.getByRole("button", { name: "Cancel queued import" }));
  fireEvent.click(screen.getByRole("button", { name: "Confirm cancellation" }));
  await screen.findByText(/running imports cannot be cancelled/);
  expect(screen.queryByText(/Cancellation was recorded/)).toBeNull();
});
it("paginates history without losing the newest scoped operation", async () => {
  setup({
    initial: { ...base, state: "FAILED", outcome: "API_FAILURE" },
    pages: true,
  });
  await screen.findByText(/Import history/);
  await waitFor(() =>
    expect(
      (
        screen.getByRole("button", {
          name: "Next imports",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(false),
  );
  fireEvent.click(screen.getByRole("button", { name: "Next imports" }));
  await screen.findByText("b".repeat(32));
  expect(screen.getByText(/Latest operation:/).textContent).toContain(base.id);
});
it("requires history inspection after session expiration", async () => {
  setup({ unauthorized: true });
  await openConfirm();
  fireEvent.click(screen.getByLabelText(/I explicitly request/));
  fireEvent.click(screen.getByRole("button", { name: "Confirm import" }));
  await screen.findByText(/Your session ended/);
  expect(screen.queryByText("Retry same import request")).toBeNull();
  fireEvent.click(
    screen.getByRole("button", { name: "Refresh and inspect history" }),
  );
  await screen.findByText("I inspected import history");
});
it("does not allow uncertainty dismissal when refreshing scoped history fails", async () => {
  const { setHistoryUnavailable } = setup({ unauthorized: true });
  await openConfirm();
  fireEvent.click(screen.getByLabelText(/I explicitly request/));
  fireEvent.click(screen.getByRole("button", { name: "Confirm import" }));
  await screen.findByText(/Your session ended/);
  setHistoryUnavailable(true);
  fireEvent.click(
    screen.getByRole("button", { name: "Refresh and inspect history" }),
  );
  await screen.findByText(/Import history is unavailable/);
  expect(screen.queryByText("I inspected import history")).toBeNull();
});
it("does not create a new intent on idempotency conflict", async () => {
  const { fetcher } = setup({ conflict: true });
  await openConfirm();
  fireEvent.click(screen.getByLabelText(/I explicitly request/));
  fireEvent.click(screen.getByRole("button", { name: "Confirm import" }));
  await screen.findByText(/IDEMPOTENCY_CONFLICT/);
  expect(screen.queryByText("Retry same import request")).toBeNull();
  expect(
    fetcher.mock.calls.filter(
      ([p, init]) => p === "/api/imports" && init?.method === "POST",
    ),
  ).toHaveLength(1);
});
