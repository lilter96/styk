import { afterEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { OrdersAccrualReport, type AccrualReport } from "./OrdersAccrualReport";
import { orderScopeBody } from "./OrdersTypes";
const scope = { tenant: "accrual-test", moy: "moy", ozon: "ozon" };
const data = (kind: AccrualReport["kind"] = "TYPES"): AccrualReport => ({
  scope: orderScopeBody(scope),
  source_family: "ACCRUAL",
  kind,
  request: { kind },
  facts: [
    {
      path: "accrual_types[0].id",
      kind: "INTEGER",
      presence: "value",
      number: "9007199254740993",
    },
    {
      path: "accrual_types[0].name",
      kind: "STRING",
      presence: "value",
      text: "SYNTHETIC-ITEM-FEE",
    },
  ],
  unknown_fields: [],
  complete: true,
  pages: 1,
  record_count: 1,
  fingerprint: "a".repeat(64),
  observed_at: new Date().toISOString(),
  blockers: [],
});
const json = (value: unknown) =>
  new Response(JSON.stringify(value), { status: 200 });
const clients: QueryClient[] = [];
afterEach(() => {
  cleanup();
  clients.forEach((client) => client.clear());
  clients.length = 0;
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
function mount() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  clients.push(client);
  return render(
    <QueryClientProvider client={client}>
      <OrdersAccrualReport scope={scope} />
    </QueryClientProvider>,
  );
}
it("keeps accrual types and exact values separate from realization and performs only an explicit read", async () => {
  const fetcher = vi.fn(async (path: string) => {
    expect(path).toContain("/finance/accrual/types?");
    return json(data());
  });
  vi.stubGlobal("fetch", fetcher);
  mount();
  expect(fetcher).not.toHaveBeenCalled();
  expect(
    screen.getByText(
      /Начисления не прибавляются к отчёту о реализации и не подтверждают выплату/,
    ),
  ).toBeTruthy();
  fireEvent.click(
    screen.getByRole("button", { name: "Прочитать типы начислений" }),
  );
  await screen.findByText("Справочник типов начислений");
  fireEvent.click(screen.getByText("Поля источника (2)"));
  expect(screen.getByText("9007199254740993")).toBeTruthy();
  expect(fetcher.mock.calls[0][0]).toContain("/finance/accrual/types?");
});
it("reads a selected date and keeps an empty source distinct from zero revenue or payout", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string) => {
      const value = data("BY_DAY");
      value.request.date = new URL(path, "http://localhost").searchParams.get(
        "date",
      )!;
      value.record_count = 0;
      value.facts = [
        { path: "items", kind: "ARRAY", presence: "empty", size: 0 },
      ];
      return json(value);
    }),
  );
  mount();
  fireEvent.change(screen.getByLabelText("Дата начислений"), {
    target: { value: "2026-10-01" },
  });
  fireEvent.click(
    screen.getByRole("button", { name: "Прочитать начисления за день" }),
  );
  await screen.findByText("2026-10-01");
  expect(
    screen.getByText(/В ответе нет записей. Это не доказывает нулевую выручку/),
  ).toBeTruthy();
  fireEvent.click(screen.getByText("Поля источника (1)"));
  expect(screen.getByText("Явно пустое значение")).toBeTruthy();
});
it.each(["foreign", "family"])(
  "rejects %s data before presenting another source",
  async (mode) => {
    const value = data();
    if (mode === "foreign") value.scope = { ...value.scope, tenant: "foreign" };
    else value.source_family = "REALIZATION" as "ACCRUAL";
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => json(value)),
    );
    mount();
    fireEvent.click(
      screen.getByRole("button", { name: "Прочитать типы начислений" }),
    );
    await screen.findByRole("alert");
    expect(
      screen.queryByRole("region", { name: "Источник начислений Ozon" }),
    ).toBeNull();
  },
);
