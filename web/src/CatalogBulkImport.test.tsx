import { StrictMode } from "react";
import "@testing-library/jest-dom/vitest";
import { afterEach, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { CatalogBulkImport } from "./CatalogBulkImport";
import { bulkImportPlan } from "./CatalogBulkPlan";
import { prepareBulkImport, runBulkImport } from "./CatalogBulkRequests";
import {
  scopeBody,
  type CatalogConfig,
  type CatalogProduct,
  type CatalogWork,
  type Decision,
  type Enrollment,
  type Scope,
} from "./CatalogSyncTypes";

const scope: Scope = {
  tenant: "synthetic-merchant",
  moy: "synthetic-moy",
  ozon: "900001",
};
const config = (): CatalogConfig => ({
  scope: scopeBody(scope),
  revision: 0,
  enabled: false,
  test_enabled: false,
  publication_available: true,
  interval_seconds: 300,
  rules: {},
  targets: [],
  updated_at: "",
});
const product = (id: string): CatalogProduct => ({
  identity: {
    account: { provider: "ozon", id: scope.ozon },
    entity_type: "product",
    id,
    offer_id: `offer-${id}`,
  },
  fields: { name: { state: "known", data: `Товар ${id}` } },
  revision: "source-1",
  lifecycle: "active",
});
const decision = (target: Enrollment): Decision => ({
  enrollment_id: target.id,
  source: product(target.source.id),
  target: { ...product(""), identity: target.target },
  target_absent: true,
  ready: true,
  validation: [],
  commands: [
    { action: "create", target: target.target, values: target.create!.values },
  ],
  plan: {
    mapping_id: "",
    mapping_version: 0,
    fingerprint: "",
    changes: [],
    conflicts: [],
    skipped: [],
    noop: false,
  },
});
function work(id: string, state: string, code?: string): CatalogWork {
  const target = bulkImportPlan(scope, config(), [product(id)], [], [])
    .pending[0];
  return {
    id: `work-${id}`,
    enrollment_id: target.id,
    state,
    code,
    command: {
      target: target.target,
      action: "create",
      values: target.create!.values,
    },
    decision: decision(target),
    receipt: { state: "accepted" },
  };
}
const response = (value: unknown) =>
  new Response(JSON.stringify(value), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

it("preserves archived cards, skips existing products and fences unresolved outcomes", () => {
  const products = ["1", "2", "3", "4", "5", "6"].map(product);
  products[4].lifecycle = "archived";
  const plan = bulkImportPlan(
    scope,
    config(),
    products,
    [],
    [
      work("1", "RECONCILED"),
      work("2", "UNKNOWN"),
      work("3", "BLOCKED", "SOURCE_OR_TARGET_CHANGED"),
      work("4", "BLOCKED", "READBACK_SOURCE_UNAVAILABLE"),
    ],
  );
  expect(plan.products.map((item) => item.identity.id)).toEqual([
    "3",
    "5",
    "6",
  ]);
  expect(
    plan.pending.find((item) => item.source.id === "5")?.create?.values
      .lifecycle,
  ).toEqual({ state: "known", data: "archived" });
  expect([plan.existing, plan.unresolved, plan.archived]).toEqual([1, 2, 1]);
  expect(
    plan.pending.every(
      (target) =>
        !target.automatic &&
        target.create?.values.article.data === target.source.offer_id,
    ),
  ).toBe(true);
  const foreign = product("7");
  foreign.identity.account.id = "another-account";
  expect(() => bulkImportPlan(scope, config(), [foreign], [], [])).toThrow(
    "foreign",
  );
});

it("uses only the stated creation fields and keeps the durable marker on a safe pre-dispatch retry", () => {
  const first = bulkImportPlan(scope, config(), [product("1")], [], []);
  const enrolled = first.pending[0];
  enrolled.create!.values.price = { state: "known", data: "100" };
  enrolled.create!.extensions = {
    "moysklad.code": { state: "known", data: "private-code" },
  };
  const configured = { ...config(), targets: [enrolled] };
  const next = bulkImportPlan(
    scope,
    configured,
    [product("1")],
    [],
    [work("1", "BLOCKED", "SOURCE_OR_TARGET_CHANGED")],
  );
  expect(next.pending[0].create?.id).toBe(enrolled.create!.id);
  expect(Object.keys(next.pending[0].create!.values).sort()).toEqual([
    "article",
    "name",
  ]);
  expect(next.pending[0].create?.extensions).toBeUndefined();
});

it("loads all source pages before presenting an import and rejects foreign history", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const url = new URL(String(input), "http://localhost");
      if (url.pathname.endsWith("operations"))
        return response({ operations: [] });
      return response(
        url.searchParams.get("cursor")
          ? { products: [product("2")] }
          : { products: [product("1")], next_cursor: "next-page" },
      );
    }),
  );
  const plan = await prepareBulkImport(
    scope,
    config(),
    new AbortController().signal,
  );
  expect(plan.pending).toHaveLength(2);
  expect(fetch).toHaveBeenCalledTimes(3);
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) =>
      response(
        String(input).includes("operations")
          ? {
              operations: [
                { scope: { ...scopeBody(scope), tenant: "foreign" } },
              ],
            }
          : { products: [] },
      ),
    ),
  );
  await expect(
    prepareBulkImport(scope, config(), new AbortController().signal),
  ).rejects.toThrow("foreign");
});

function server(plan: ReturnType<typeof bulkImportPlan>, failApproval = false) {
  return vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input);
    if (!init?.body)
      return response(
        path.includes("operations")
          ? { operations: [] }
          : { products: plan.products },
      );
    const body = JSON.parse(init.body as string);
    if (path.endsWith("config"))
      return response({ ...body.config, revision: 1 });
    const target = plan.pending.find(
      (item) => item.id === body.enrollment_ids[0],
    )!;
    if (path.endsWith("preview"))
      return response({
        id: `preview-${target.id}`,
        scope: scopeBody(scope),
        config_revision: 1,
        decisions: [decision(target)],
      });
    if (path.endsWith("approve")) {
      if (failApproval) throw new TypeError("network outcome unknown");
      return response({
        id: `operation-${target.id}`,
        scope: scopeBody(scope),
        targets: [{ enrollment_id: target.id }],
      });
    }
    throw new Error("unexpected endpoint");
  });
}

it("stops after an unknown approval outcome without retrying or sending later products", async () => {
  const plan = bulkImportPlan(
    scope,
    config(),
    [product("1"), product("2")],
    [],
    [],
  );
  const fetcher = server(plan, true);
  vi.stubGlobal("fetch", fetcher);
  const result = await runBulkImport(
    scope,
    config(),
    plan,
    new AbortController().signal,
    vi.fn(),
    vi.fn(),
  );
  expect(result).toEqual({ queued: 0, failed: [], uncertain: true });
  expect(
    fetcher.mock.calls.filter(([path]) => String(path).endsWith("approve")),
  ).toHaveLength(1);
  expect(
    fetcher.mock.calls.filter(([path]) => String(path).endsWith("preview")),
  ).toHaveLength(1);
});

it.each(["changed configuration", "left account"])(
  "does not enqueue a stale import after %s",
  async (reason) => {
    const plan = bulkImportPlan(scope, config(), [product("1")], [], []);
    const fetcher = server(plan);
    vi.stubGlobal("fetch", fetcher);
    const controller = new AbortController();
    if (reason === "left account") controller.abort();
    const current =
      reason === "changed configuration"
        ? { ...config(), revision: 1 }
        : config();
    await expect(
      runBulkImport(scope, current, plan, controller.signal, vi.fn(), vi.fn()),
    ).rejects.toThrow();
    expect(fetcher).not.toHaveBeenCalled();
  },
);

it("a new merchant imports the whole source list with one explicit confirmation through normal STYK endpoints", async () => {
  const plan = bulkImportPlan(
    scope,
    config(),
    [product("1"), product("2")],
    [],
    [],
  );
  // Reuse the configured create identities returned by the UI's preparation.
  const fetcher = server(plan);
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      if (String(input).endsWith("config") && init?.body) {
        plan.pending = JSON.parse(init.body as string).config.targets;
      }
      return fetcher(input, init);
    }),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <StrictMode>
      <QueryClientProvider client={client}>
        <CatalogBulkImport
          scope={scope}
          config={config()}
          disabled={false}
          onSaved={vi.fn()}
          onBusy={vi.fn()}
        />
      </QueryClientProvider>
    </StrictMode>,
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Импортировать все товары" }),
  );
  const start = await screen.findByRole("button", {
    name: "Начать импорт (2)",
  });
  expect(start).toBeDisabled();
  expect(fetcher.mock.calls.every(([, init]) => !init?.body)).toBe(true);
  fireEvent.click(
    screen.getByRole("checkbox", { name: /Разрешаю создать все/ }),
  );
  fireEvent.click(start);
  await waitFor(() =>
    expect(screen.getByRole("status")).toHaveTextContent(
      "Передано на отправку: 2",
    ),
  );
  expect(
    fetcher.mock.calls.filter(([path]) => String(path).endsWith("approve")),
  ).toHaveLength(2);
  expect(
    fetcher.mock.calls.every(([path]) =>
      String(path).startsWith("/api/catalog-sync/"),
    ),
  ).toBe(true);
});
