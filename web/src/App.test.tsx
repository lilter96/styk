import { afterEach, describe, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { Diagnostics, Overview, Products, StockRows } from "./App";
import type { Report } from "./api";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});
const scope = { tenant: "test-tenant", moy: "test-moy", ozon: "test-ozon" };
function mount(node: React.ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter>{node}</MemoryRouter>
    </QueryClientProvider>,
  );
}
const response = (data: unknown) =>
  Promise.resolve(
    new Response(JSON.stringify(data), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    }),
  );

describe("read-only views", () => {
  it("shows loading and missing workspace states", () => {
    const x = mount(<Overview loading error={null} />);
    expect(screen.getByRole("status").textContent).toContain("Loading");
    x.rerender(
      <QueryClientProvider client={new QueryClient()}>
        <MemoryRouter>
          <Overview loading={false} error={null} />
        </MemoryRouter>
      </QueryClientProvider>,
    );
    expect(screen.getByText(/select both registered/i)).toBeTruthy();
  });
  it("shows future observation warnings and review actions", () => {
    const report: Report = {
      checks: [
        {
          code: "MOYSKLAD_IMPORT",
          status: "WARN",
          detail: "Stale or invalid observation",
        },
      ],
      batches: {
        moysklad: {
          present: true,
          completed: true,
          age: "-1m",
          observed_at: "2026-10-29T00:00:00Z",
          counts: { products: 9, variants: 2, warehouses: 3, stock_rows: 33 },
        },
      },
      decision_status: "NOT_CHECKED",
      decision_reasons: [],
      publication: "DISABLED",
      mapping_counts: {},
    };
    mount(<Overview report={report} loading={false} error={null} />);
    expect(
      screen.getByText("Observation timestamp is in the future"),
    ).toBeTruthy();
    expect(screen.getByText(/Imported .*2026/)).toBeTruthy();
    expect(screen.getByRole("link", { name: "Review products" })).toBeTruthy();
    expect(
      screen.getByRole("link", { name: "Review warehouses and stock" }),
    ).toBeTruthy();
  });
  it("shows backend failures", () => {
    mount(<Overview loading={false} error={new Error("Offline")} />);
    expect(screen.getByRole("alert").textContent).toBe("Offline");
  });
  it("filters products on the server and shows ambiguity", async () => {
    const spy = vi.spyOn(globalThis, "fetch").mockImplementation(() =>
      response({
        items: [
          {
            product_id: "1",
            offer_id: "sample",
            sku: null,
            archived: false,
            target_kind: null,
            target_id: null,
            match_status: "AMBIGUOUS",
            candidate_count: 2,
            candidates: [
              { kind: "product", id: "a", evidence: ["article"] },
              { kind: "variant", id: "b", evidence: ["code"] },
            ],
            candidates_truncated: false,
          },
        ],
        offset: 0,
        has_next: false,
      }),
    );
    mount(<Products scope={scope} />);
    expect(await screen.findByText("ambiguous")).toBeTruthy();
    expect(screen.getByText("Unknown")).toBeTruthy();
    fireEvent.click(screen.getByText("2 candidates"));
    expect(screen.getByText(/variant: b/)).toBeTruthy();
    fireEvent.change(screen.getByPlaceholderText(/offer id/i), {
      target: { value: "query" },
    });
    await waitFor(() =>
      expect(
        spy.mock.calls.some((c) => String(c[0]).includes("search=query")),
      ).toBe(true),
    );
  });
  it("reloads product observations for a changed account", async () => {
    const spy = vi
      .spyOn(globalThis, "fetch")
      .mockImplementation(() =>
        response({ items: [], offset: 0, has_next: false }),
      );
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    const view = (ozon: string) => (
      <QueryClientProvider client={client}>
        <MemoryRouter>
          <Products scope={{ ...scope, ozon }} />
        </MemoryRouter>
      </QueryClientProvider>
    );
    const rendered = render(view("first-account"));
    await waitFor(() =>
      expect(
        spy.mock.calls.some((c) =>
          String(c[0]).includes("ozon_account=first-account"),
        ),
      ).toBe(true),
    );
    rendered.rerender(view("second-account"));
    await waitFor(() =>
      expect(
        spy.mock.calls.some((c) =>
          String(c[0]).includes("ozon_account=second-account"),
        ),
      ).toBe(true),
    );
  });
  it("keeps missing stock unknown and renders zero", async () => {
    vi.spyOn(globalThis, "fetch").mockImplementation(() =>
      response({
        items: [
          {
            kind: "product",
            item_id: "a",
            stock: "0",
            reserve: "0",
            incoming: "0",
            quantity: null,
            present: null,
            reserved: null,
            free_stock: null,
            observed_at: new Date().toISOString(),
          },
        ],
        offset: 0,
        has_next: false,
      }),
    );
    mount(
      <StockRows
        scope={scope}
        source="moysklad"
        account="test-moy"
        warehouse="w"
      />,
    );
    expect(await screen.findByText(/Physical 0/)).toBeTruthy();
    expect(screen.getByText(/Available Unknown/)).toBeTruthy();
  });
  it("renders diagnostic reasons without inventing a quantity", () => {
    const report: Report = {
      checks: [
        { code: "COVERAGE", status: "WARN", detail: "Incomplete evidence." },
      ],
      batches: {},
      decision_status: "UNKNOWN",
      decision_reasons: ["SOURCE_BASELINE_NOT_PROVEN"],
      publication: "DISABLED",
      mapping_counts: {},
    };
    mount(<Diagnostics report={report} loading={false} error={null} />);
    expect(screen.getByText("SOURCE_BASELINE_NOT_PROVEN")).toBeTruthy();
    expect(screen.getByText("Incomplete evidence.")).toBeTruthy();
    expect(screen.getByText("unknown")).toBeTruthy();
  });
});

it("reviews candidates and requires a durable review before confirmation", async () => {
  vi.spyOn(globalThis, "fetch").mockImplementation((input) => {
    const path = String(input);
    if (path.includes("moysklad-products"))
      return response({
        items: [{ kind: "variant", id: "v", article: "shared", code: "c" }],
        offset: 0,
        has_next: false,
      });
    if (path.includes("product-mapping-history"))
      return response({ items: [], offset: 0, has_next: false });
    return response({
      items: [
        {
          product_id: "p",
          offer_id: "shared",
          sku: null,
          archived: false,
          target_kind: null,
          target_id: null,
          match_status: "ONE_CANDIDATE",
          candidate_count: 1,
          candidates: [{ kind: "variant", id: "v", evidence: ["article"] }],
          candidates_truncated: false,
        },
      ],
      offset: 0,
      has_next: false,
    });
  });
  mount(<Products scope={scope} />);
  fireEvent.click(await screen.findByRole("button", { name: "Review" }));
  expect(await screen.findByText(/variant · v — evidence/)).toBeTruthy();
  fireEvent.click(await screen.findByRole("button", { name: "variant · v" }));
  expect(screen.getByText(/DETERMINISTIC_CANDIDATE: article/)).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Confirm mapping" })).toBeNull();
  expect(await screen.findByText(/No confirmed mapping history/)).toBeTruthy();
});
