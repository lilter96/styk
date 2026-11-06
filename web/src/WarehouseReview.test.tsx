import { afterEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { WarehouseReview } from "./WarehouseReview";
const scope = { tenant: "tenant", moy: "moy", ozon: "ozon" };
const evidence = {
  name: "Склад",
  status: "created",
  is_rfbs: false,
  stock_rows: 0,
  observed_at: new Date().toISOString(),
};
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});
function mount(status: string, rfbs: boolean | null = false) {
  const fetcher = vi.spyOn(globalThis, "fetch").mockImplementation(
    async () =>
      new Response(
        JSON.stringify({
          status,
          moysklad: { ...evidence, id: "m", account: scope.moy },
          ozon: { ...evidence, id: "o", account: scope.ozon, is_rfbs: rfbs },
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
  const view = render(
    <QueryClientProvider client={client}>
      <WarehouseReview scope={scope} moy="m" ozon="o" />
    </QueryClientProvider>,
  );
  return { fetcher, view, client };
}
for (const [status, text] of [
  ["ELIGIBLE_FOR_REVIEW", "Проверьте сопоставление"],
  ["ALREADY_MAPPED", "Склады уже сопоставлены"],
  ["MAPPING_CONFLICT", "Обнаружен конфликт сопоставлений"],
  ["UNSUPPORTED_WAREHOUSE", "Возможность сопоставления не подтверждена"],
  ["MISSING_SOURCE_EVIDENCE", "Не хватает данных о складах"],
])
  it(`reads ${status} without creating a review or mapping`, async () => {
    const { fetcher } = mount(status);
    await screen.findByText(status);
    expect(screen.getByRole("heading", { name: text })).toBeTruthy();
    expect(
      screen.queryByRole("button", { name: "Подтвердить сопоставление" }),
    ).toBeNull();
    expect(
      fetcher.mock.calls.every(
        ([, init]) => !init?.method || init.method === "GET",
      ),
    ).toBe(true);
  });
it("explains an existing pair and closes without repeat confirmation", async () => {
  mount("ALREADY_MAPPED");
  await screen.findByText(/Повторное сопоставление не требуется/);
  expect(
    screen.queryByRole("button", { name: "Повторить проверку" }),
  ).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Готово" }));
  expect(screen.queryByRole("dialog")).toBeNull();
});
it("uses backend capability for rFBS and never enables publication", async () => {
  mount("ELIGIBLE_FOR_REVIEW", true);
  const button = await screen.findByRole("button", {
    name: "Повторить проверку",
  });
  expect((button as HTMLButtonElement).disabled).toBe(false);
  expect(
    screen.getByText(/Само по себе оно не включает синхронизацию остатков/),
  ).toBeTruthy();
});
it("does not accept unknown warehouse classification", async () => {
  mount("ELIGIBLE_FOR_REVIEW", null);
  const button = await screen.findByRole("button", {
    name: "Повторить проверку",
  });
  expect((button as HTMLButtonElement).disabled).toBe(true);
});
it("refetches pair evidence after an account changes", async () => {
  const { fetcher, view, client } = mount("MISSING_SOURCE_EVIDENCE");
  await screen.findByText("MISSING_SOURCE_EVIDENCE");
  view.rerender(
    <QueryClientProvider client={client}>
      <WarehouseReview
        scope={{ ...scope, ozon: "different" }}
        moy="m"
        ozon="o"
      />
    </QueryClientProvider>,
  );
  await screen.findByText("MISSING_SOURCE_EVIDENCE");
  expect(
    fetcher.mock.calls.some(([url]) =>
      String(url).includes("ozon_account=different"),
    ),
  ).toBe(true);
});
it("reports a failed read with a retry", async () => {
  vi.spyOn(globalThis, "fetch").mockImplementation(
    async () => new Response("", { status: 503 }),
  );
  render(
    <QueryClientProvider client={new QueryClient()}>
      <WarehouseReview scope={scope} moy="m" ozon="o" />
    </QueryClientProvider>,
  );
  expect(await screen.findByRole("alert")).toBeTruthy();
  expect(screen.getByRole("button", { name: "Повторить запрос" })).toBeTruthy();
});
it("shows loading while reading a pair", () => {
  vi.spyOn(globalThis, "fetch").mockImplementation(() => new Promise(() => {}));
  render(
    <QueryClientProvider client={new QueryClient()}>
      <WarehouseReview scope={scope} moy="m" ozon="o" />
    </QueryClientProvider>,
  );
  expect(screen.getByRole("status").textContent).toContain("Проверяем склады");
});
