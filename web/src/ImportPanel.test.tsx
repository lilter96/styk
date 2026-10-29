import { useState } from "react";
import { afterEach, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import type { Batch } from "./api";
import { ImportPanel } from "./ImportPanel";
import {
  ImportSubmissionNotice,
  useImportSubmission,
} from "./ImportSubmission";
import type { ImportOperation, ImportScope, ImportProvider } from "./api";
const scope: ImportScope = {
  provider: "ozon",
  tenant: "tenant-a",
  account: "ozon-a",
};
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
function Harness({
  client,
  initialScope = scope,
  configured = true,
  latestBatch,
}: {
  client: QueryClient;
  initialScope?: ImportScope;
  configured?: boolean;
  latestBatch?: Batch;
}) {
  const [current, setCurrent] = useState<ImportScope>(initialScope);
  const submission = useImportSubmission();
  return (
    <>
      <button
        onClick={() =>
          setCurrent({
            provider: current.provider,
            tenant: "tenant-b",
            account: "ozon-b",
          })
        }
      >
        Switch scope
      </button>
      <button
        onClick={() =>
          setCurrent({
            ...current,
            provider: current.provider === "ozon" ? "moysklad" : "ozon",
          })
        }
      >
        Switch provider
      </button>
      <ImportSubmissionNotice
        submission={submission}
        scope={current}
        restore={setCurrent}
      />
      <ImportPanel
        key={`${current.tenant}:${current.provider}:${current.account}`}
        scope={current}
        submission={submission}
        configured={configured}
        latestBatch={latestBatch}
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
    provider?: ImportProvider;
    latestBatch?: Batch;
    configured?: boolean;
    initial?: ImportOperation;
    lost?: boolean;
    unauthorized?: boolean;
    conflict?: boolean;
    cancelConflict?: boolean;
    pages?: boolean;
  } = {},
) {
  const fixtureScope: ImportScope = {
    ...scope,
    provider: opts.provider ?? "ozon",
  };
  const fixtureBase: ImportOperation = {
    ...base,
    provider: fixtureScope.provider,
    operation_type:
      fixtureScope.provider === "moysklad"
        ? "MOYSKLAD_CATALOG_IMPORT"
        : "OZON_CATALOG_IMPORT",
  };
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
        operation = { ...fixtureBase };
        if (opts.lost && attempts === 1) throw new TypeError("connection lost");
        return new Response(
          JSON.stringify({ operation, idempotent_replay: !!opts.lost }),
          { status: attempts === 1 ? 201 : 200 },
        );
      }
      if (init?.method === "POST" && path.endsWith("/cancel")) {
        if (opts.cancelConflict) {
          operation = {
            ...fixtureBase,
            state: "RUNNING",
            phase: "READING_PRODUCTS",
          };
          return new Response('{"error":"CANCELLATION_UNAVAILABLE"}', {
            status: 409,
          });
        }
        operation = {
          ...fixtureBase,
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
        if (
          query.get("tenant") !== fixtureScope.tenant ||
          (query.has("moysklad_account") ? "moysklad" : "ozon") !==
            fixtureScope.provider
        )
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
        <Harness
          client={client}
          initialScope={fixtureScope}
          configured={opts.configured ?? true}
          latestBatch={opts.latestBatch}
        />
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
async function openConfirm(name = "Ozon") {
  fireEvent.click(
    await screen.findByRole("button", { name: "Import catalog" }),
  );
  await screen.findByRole("dialog", { name: `Confirm ${name} catalog import` });
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
  const key = [
    "import-detail",
    scope.tenant,
    scope.provider,
    scope.account,
    base.id,
  ];
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
  client.setQueryData(
    ["import-history", scope.tenant, scope.provider, scope.account, 0],
    {
      items: [],
      has_next: false,
      offset: 0,
    },
  );
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

const moyBase: ImportOperation = {
  ...base,
  provider: "moysklad",
  operation_type: "MOYSKLAD_CATALOG_IMPORT",
  counts: { ...base.counts, variants: 1, warehouse_observations: 2 },
  limits: { ...base.limits, variants: 1000, warehouse_observations: 10000 },
};
it("submits MoySklad only after acknowledgement and preserves its exact intent across providers", async () => {
  const { fetcher, client } = setup({ provider: "moysklad", lost: true });
  await screen.findByText(/No import operations/);
  expect(fetcher.mock.calls.some(([, init]) => init?.method === "POST")).toBe(
    false,
  );
  await openConfirm("MoySklad");
  expect(
    within(screen.getByRole("dialog")).getByText(
      /Customer orders are not imported/,
    ),
  ).toBeTruthy();
  expect(
    (
      screen.getByRole("button", {
        name: "Confirm import",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  fireEvent.click(screen.getByLabelText(/I explicitly request/));
  const confirmButton = screen.getByRole("button", { name: "Confirm import" });
  fireEvent.click(confirmButton);
  fireEvent.click(confirmButton);
  await screen.findByText(/Submission outcome unknown/);
  fireEvent.click(screen.getByText("Switch scope"));
  expect(screen.queryByText("Retry same import request")).toBeNull();
  fireEvent.click(screen.getByText("Return to original import scope"));
  fireEvent.click(screen.getByText("Switch provider"));
  await screen.findByRole("region", { name: "Ozon catalog imports" });
  expect(screen.queryByText("Retry same import request")).toBeNull();
  fireEvent.click(screen.getByText("Return to original import scope"));
  fireEvent.click(screen.getByText("Retry same import request"));
  await screen.findByText(/existing import operation was recovered/);
  const calls = fetcher.mock.calls.filter(
    ([url, init]) => url === "/api/imports" && init?.method === "POST",
  );
  expect(calls).toHaveLength(2);
  expect(calls[0][1]?.body).toBe(calls[1][1]?.body);
  const body = JSON.parse(String(calls[0][1]?.body));
  expect(body).toMatchObject({
    tenant: scope.tenant,
    moysklad_account: scope.account,
    operation_type: "MOYSKLAD_CATALOG_IMPORT",
    acknowledge: true,
  });
  expect(body.ozon_account).toBeUndefined();
  expect(body.limits).toBeUndefined();
  expect(body.idempotency_key).toMatch(/^[0-9a-f-]{36}$/);
  expect(
    client
      .getQueryCache()
      .findAll()
      .some(
        (q) =>
          JSON.stringify(q.queryKey) ===
          JSON.stringify([
            "import-history",
            scope.tenant,
            "moysklad",
            scope.account,
            0,
          ]),
      ),
  ).toBe(true);
  expect(
    client
      .getQueryCache()
      .findAll()
      .some(
        (q) =>
          JSON.stringify(q.queryKey) ===
          JSON.stringify([
            "import-history",
            scope.tenant,
            "ozon",
            scope.account,
            0,
          ]),
      ),
  ).toBe(true);
});
it("keeps MoySklad history readable when credentials are missing", async () => {
  setup({
    provider: "moysklad",
    configured: false,
    initial: {
      ...moyBase,
      state: "FAILED",
      outcome: "CONFIGURATION_UNAVAILABLE",
    },
  });
  await screen.findByText(/matching worker configuration is unavailable/);
  expect(
    (
      screen.getByRole("button", {
        name: "Import catalog",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
});
it("renders MoySklad stages and attempt-local observation counters", async () => {
  const { client } = setup({
    provider: "moysklad",
    initial: {
      ...moyBase,
      state: "RUNNING",
      phase: "READING_VARIANTS",
      attempt: 1,
    },
  });
  await screen.findByText(/variants observed 1/);
  const key = [
    "import-detail",
    scope.tenant,
    "moysklad",
    scope.account,
    base.id,
  ];
  for (const phase of [
    "READING_PRODUCTS",
    "READING_VARIANTS",
    "READING_WAREHOUSES",
    "READING_AGGREGATE_STOCK",
    "READING_WAREHOUSE_STOCK",
    "PERSISTING",
  ]) {
    client.setQueryData(key, {
      ...moyBase,
      state: "RUNNING",
      phase,
      attempt: 2,
      counts: { ...moyBase.counts, variants: 0 },
    });
    await screen.findByText(new RegExp(`Phase: ${phase}`));
    await screen.findByText(/variants observed 0/);
  }
  expect(screen.getByText(/attempt-local/)).toBeTruthy();
  expect(screen.getByText(/warehouse stock observations 2/)).toBeTruthy();
});
for (const state of [
  "QUEUED",
  "FAILED",
  "OUTCOME_UNKNOWN",
  "CANCELLED",
] as const)
  it(`renders MoySklad ${state} without displaying a saved batch`, async () => {
    setup({
      provider: "moysklad",
      initial: {
        ...moyBase,
        state,
        outcome: state === "FAILED" ? "API_FAILURE" : null,
      },
    });
    await screen.findByRole("region", { name: "Import operation details" });
    await screen.findByRole("heading", { name: /Operation / });
    expect(screen.queryByText(/Completed batch:/)).toBeNull();
  });
it("shows MoySklad reused batch without claiming fresh timestamps and invalidates data", async () => {
  const { invalidate } = setup({
    provider: "moysklad",
    initial: {
      ...moyBase,
      state: "SUCCEEDED",
      phase: "COMPLETED",
      batch_id: 51,
      batch_created: false,
      outcome: "COMPLETE_TRAVERSAL",
    },
  });
  await screen.findByText(/observation timestamps unchanged/);
  await waitFor(() =>
    expect(
      invalidate.mock.calls.some(([arg]) =>
        JSON.stringify(arg).includes('"diagnostics"'),
      ),
    ).toBe(true),
  );
  expect(screen.getByText(/variants observed 1/)).toBeTruthy();
});
it("cancels a MoySklad queued operation using only its provider scope", async () => {
  const { fetcher } = setup({ provider: "moysklad", initial: moyBase });
  await screen.findByText(/Waiting for an available worker/);
  fireEvent.click(screen.getByRole("button", { name: "Cancel queued import" }));
  fireEvent.click(screen.getByRole("button", { name: "Confirm cancellation" }));
  await screen.findByText(/Cancellation was recorded/);
  const call = fetcher.mock.calls.find(([url]) =>
    String(url).endsWith("/cancel"),
  )!;
  expect(JSON.parse(String(call[1]?.body))).toEqual({
    tenant: scope.tenant,
    moysklad_account: scope.account,
    acknowledge: true,
  });
});
it("handles a MoySklad queued-to-running cancellation race", async () => {
  setup({ provider: "moysklad", initial: moyBase, cancelConflict: true });
  await screen.findByText(/Waiting for an available worker/);
  fireEvent.click(screen.getByRole("button", { name: "Cancel queued import" }));
  fireEvent.click(screen.getByRole("button", { name: "Confirm cancellation" }));
  await screen.findByText(/running imports cannot be cancelled/);
  expect(screen.queryByText(/Cancellation was recorded/)).toBeNull();
});

it("keeps simultaneous histories isolated when both providers use the same account text", async () => {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
    const url = String(input),
      query = new URLSearchParams(url.split("?")[1]);
    const provider = query.has("moysklad_account") ? "moysklad" : "ozon";
    const op = {
      ...(provider === "moysklad" ? moyBase : base),
      id: provider === "moysklad" ? "m".repeat(32) : base.id,
    };
    return new Response(
      JSON.stringify(
        url.startsWith("/api/imports?")
          ? { items: [op], has_next: false, offset: 0 }
          : op,
      ),
      { status: 200 },
    );
  });
  function Pair() {
    const submission = useImportSubmission();
    return (
      <>
        {(["moysklad", "ozon"] as const).map((provider) => (
          <ImportPanel
            key={provider}
            scope={{ ...scope, provider }}
            configured
            submission={submission}
          />
        ))}
      </>
    );
  }
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <Pair />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  const moy = await screen.findByRole("region", {
    name: "MoySklad catalog imports",
  });
  const ozon = await screen.findByRole("region", {
    name: "Ozon catalog imports",
  });
  await within(moy).findByRole("heading", { name: /Operation m/ });
  await within(ozon).findByRole("heading", { name: /Operation a/ });
  expect(
    within(ozon).queryByRole("heading", { name: /Operation m/ }),
  ).toBeNull();
  expect(
    within(moy).queryByRole("heading", { name: /Operation a/ }),
  ).toBeNull();
});

it("keeps the selected MoySklad account's prior persisted import visible after a failed operation", async () => {
  setup({
    provider: "moysklad",
    initial: { ...moyBase, state: "FAILED", outcome: "API_FAILURE" },
    latestBatch: {
      present: true,
      completed: true,
      age: "2h",
      observed_at: "2026-01-01T00:00:00Z",
      counts: { products: 9, variants: 2, warehouses: 3, stock_rows: 0 },
    },
  });
  await screen.findByText("Latest completed import");
  await screen.findByText(
    /Products 9; variants 2; warehouses 3; warehouse stock rows 0/,
  );
  await screen.findByText(/Import failed/);
  expect(screen.queryByText(/Completed batch:/)).toBeNull();
});
