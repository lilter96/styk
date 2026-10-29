import { afterEach, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { WarehouseReview } from "./WarehouseReview";
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});
const scope = { tenant: "tenant", moy: "moy", ozon: "ozon" };
const e = {
  id: "warehouse",
  name: "Synthetic warehouse",
  account: "account",
  status: "enabled",
  is_rfbs: false,
  observed_at: "2020-01-01T00:00:00Z",
  stock_rows: 0,
};
function mount(status: string) {
  vi.spyOn(globalThis, "fetch").mockResolvedValue(
    new Response(
      JSON.stringify({
        status,
        moysklad: e,
        ozon: { ...e, is_rfbs: status === "UNSUPPORTED_WAREHOUSE" },
        mappings:
          status === "MAPPING_CONFLICT"
            ? [
                {
                  mapping_id: 1,
                  version: 1,
                  moysklad_warehouse_id: "one",
                  ozon_warehouse_id: "other",
                  provenance: "test",
                  confirmed_at: "2020-01-01T00:00:00Z",
                },
              ]
            : [],
        history:
          status === "ALREADY_MAPPED"
            ? [
                {
                  mapping_id: 1,
                  version: 1,
                  moysklad_warehouse_id: "one",
                  ozon_warehouse_id: "two",
                  provenance: "test",
                  confirmed_at: "2020-01-01T00:00:00Z",
                },
              ]
            : [],
        has_next: false,
        offset: 0,
      }),
      { status: 200 },
    ),
  );
  render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <WarehouseReview scope={scope} moy="one" ozon="two" />
    </QueryClientProvider>,
  );
}
for (const status of [
  "ELIGIBLE_FOR_REVIEW",
  "ALREADY_MAPPED",
  "MAPPING_CONFLICT",
  "UNSUPPORTED_WAREHOUSE",
  "MISSING_SOURCE_EVIDENCE",
])
  it(`renders ${status} without a confirmation action`, async () => {
    mount(status);
    expect(await screen.findByText(status)).toBeTruthy();
    expect(
      screen.queryByRole("button", { name: /confirm mapping/i }),
    ).toBeNull();
    expect(
      screen.getAllByText(/No observation; stock is unknown/),
    ).toHaveLength(2);
    if (status === "UNSUPPORTED_WAREHOUSE")
      expect(screen.getByText(/Classification: rFBS/)).toBeTruthy();
    if (status === "ALREADY_MAPPED")
      expect(screen.getByText(/provenance: test/)).toBeTruthy();
  });
it("reports a failed read", async () => {
  vi.spyOn(globalThis, "fetch").mockResolvedValue(
    new Response("", { status: 503 }),
  );
  render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <WarehouseReview scope={scope} moy="one" ozon="two" />
    </QueryClientProvider>,
  );
  expect(await screen.findByRole("alert")).toBeTruthy();
});
it("shows missing evidence and refetches for a different account", async () => {
  const fetchMock = vi.spyOn(globalThis, "fetch").mockImplementation(
    async () =>
      new Response(
        JSON.stringify({
          status: "MISSING_SOURCE_EVIDENCE",
          moysklad: null,
          ozon: null,
          mappings: [],
          history: [],
          has_next: false,
          offset: 0,
        }),
        { status: 200 },
      ),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const component = render(
    <QueryClientProvider client={client}>
      <WarehouseReview scope={scope} moy="one" ozon="two" />
    </QueryClientProvider>,
  );
  expect(
    await screen.findByText(/MoySklad: no eligible observation/),
  ).toBeTruthy();
  component.rerender(
    <QueryClientProvider client={client}>
      <WarehouseReview
        scope={{ ...scope, ozon: "different" }}
        moy="one"
        ozon="two"
      />
    </QueryClientProvider>,
  );
  await screen.findByText(/Ozon: no eligible observation/);
  expect(
    fetchMock.mock.calls.some(([url]) =>
      String(url).includes("ozon_account=different"),
    ),
  ).toBe(true);
});
it("shows a loading state while the review is requested", () => {
  vi.spyOn(globalThis, "fetch").mockImplementation(() => new Promise(() => {}));
  render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <WarehouseReview scope={scope} moy="one" ozon="two" />
    </QueryClientProvider>,
  );
  expect(screen.getByRole("status").textContent).toContain(
    "Loading warehouse review",
  );
});
