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
import { WarehouseOverview } from "./WarehouseOverview";
import { WarehouseStockTable } from "./WarehouseStockDetail";
import { WarehouseMappingDrawer } from "./WarehouseMappingDrawer";
import { stockAge, warehouseType, type WarehouseRow } from "./WarehouseData";
import { useConfirmation } from "./Confirmation";
const scope = { tenant: "t", moy: "m", ozon: "o" };
const row: WarehouseRow = {
  id: "o/1",
  name: "Склад Екатеринбург",
  is_rfbs: false,
  warehouse_type: "fbs",
  store_id: "",
  store_name: "",
  state: "unmapped",
  stock_rows: 0,
  observed_at: null,
  mapping_id: null,
  confirmed_at: null,
};
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});
function wrap(children: React.ReactNode) {
  return render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <MemoryRouter>{children}</MemoryRouter>
    </QueryClientProvider>,
  );
}
function Overview() {
  const confirmation = useConfirmation();
  return <WarehouseOverview scope={scope} confirmation={confirmation} />;
}
function Drawer() {
  const confirmation = useConfirmation();
  return (
    <WarehouseMappingDrawer
      scope={scope}
      row={row}
      confirmation={confirmation}
      close={() => {}}
    />
  );
}
it("uses whole-scope counts and sends search/filter/page to the server", async () => {
  const fetcher = vi
    .spyOn(globalThis, "fetch")
    .mockImplementation(
      async () =>
        new Response(
          JSON.stringify({
            items: [row],
            counts: { total: 58, mapped: 13, unmapped: 44, attention: 1 },
            total: 58,
            offset: 0,
            has_next: true,
            ozon_imported: true,
            moy_imported: true,
          }),
        ),
    );
  wrap(<Overview />);
  await screen.findByRole("link", { name: row.name });
  expect(screen.getByRole("button", { name: "Все (58)" })).toBeTruthy();
  expect(
    screen.getByRole("link", { name: row.name }).getAttribute("href"),
  ).toBe("/warehouses/o%2F1");
  fireEvent.click(screen.getByRole("button", { name: "Следующая страница" }));
  await waitFor(() =>
    expect(
      fetcher.mock.calls.some(([url]) => String(url).includes("offset=25")),
    ).toBe(true),
  );
  fireEvent.change(screen.getByLabelText("Поиск склада"), {
    target: { value: "Екатеринбург" },
  });
  await waitFor(() =>
    expect(
      fetcher.mock.calls.some(([url]) => {
        const u = new URL(String(url), "http://test");
        return (
          u.searchParams.get("search") === "Екатеринбург" &&
          u.searchParams.get("offset") === "0"
        );
      }),
    ).toBe(true),
  );
  fireEvent.click(screen.getByRole("button", { name: "Без связи (44)" }));
  await waitFor(() =>
    expect(
      fetcher.mock.calls.some(([url]) =>
        String(url).includes("filter=unmapped"),
      ),
    ).toBe(true),
  );
});
it("does not replace unavailable aggregates with zero", async () => {
  vi.spyOn(globalThis, "fetch").mockResolvedValue(
    new Response("", { status: 503 }),
  );
  wrap(<Overview />);
  await screen.findByRole("alert");
  expect(screen.getByRole("button", { name: "Все (—)" })).toBeTruthy();
});
it("keeps occupied candidates disabled and performs no writes on selection", async () => {
  const fetcher = vi.spyOn(globalThis, "fetch").mockImplementation(
    async () =>
      new Response(
        JSON.stringify({
          items: [
            { id: "busy", name: "Основной", available: false },
            { id: "free", name: "Резервный", available: true },
          ],
          total: 2,
          offset: 0,
          has_next: false,
        }),
      ),
  );
  wrap(<Drawer />);
  const free = await screen.findByRole("radio", { name: /Резервный/ });
  expect(
    (screen.getByRole("radio", { name: /Основной/ }) as HTMLInputElement)
      .disabled,
  ).toBe(true);
  fireEvent.click(free);
  expect(
    (
      screen.getByRole("button", {
        name: /Проверить соответствие/,
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(false);
  expect(
    fetcher.mock.calls.every(
      ([, init]) => !init?.method || init.method === "GET",
    ),
  ).toBe(true);
});
it("preserves zero, null and source-specific columns and searches server-side", async () => {
  const fetcher = vi
    .spyOn(globalThis, "fetch")
    .mockImplementation(
      async () =>
        new Response(
          JSON.stringify({
            items: [
              {
                kind: "variant",
                item_id: "v",
                name: "Кофе",
                article: "KOF",
                stock: "0",
                reserve: "0",
                incoming: "1.25",
                quantity: null,
                present: null,
                reserved: null,
                free_stock: null,
                observed_at: "2099-01-01T00:00:00Z",
              },
            ],
            offset: 0,
            has_next: true,
          }),
        ),
    );
  wrap(
    <WarehouseStockTable scope={scope} source="moysklad" warehouse="store" />,
  );
  await screen.findByText("Кофе");
  expect(screen.getAllByText("0")).toHaveLength(2);
  expect(screen.getByText("—")).toBeTruthy();
  expect(screen.getByText("1.25")).toBeTruthy();
  expect(screen.getByText("Время указано в будущем")).toBeTruthy();
  expect(screen.getByRole("columnheader", { name: "В пути" })).toBeTruthy();
  expect(screen.queryByRole("columnheader", { name: "Свободно" })).toBeNull();
  fireEvent.change(screen.getByLabelText("Найти товар или артикул"), {
    target: { value: "KOF" },
  });
  await waitFor(() =>
    expect(
      fetcher.mock.calls.some(([url]) => String(url).includes("search=KOF")),
    ).toBe(true),
  );
});
it("does not synthesize stock rows for an empty observation", async () => {
  vi.spyOn(globalThis, "fetch").mockImplementation(
    async () =>
      new Response(JSON.stringify({ items: [], offset: 0, has_next: false })),
  );
  wrap(
    <WarehouseStockTable scope={scope} source="ozon" warehouse="warehouse" />,
  );
  await screen.findByText("Нет данных об остатках");
  expect(screen.queryByRole("table")).toBeNull();
});
it.each([
  [null, 0, "Нет данных"],
  [null, 1, "Время неизвестно"],
  ["bad", 1, "Время неизвестно"],
  ["2099-01-01", 1, "Время указано в будущем"],
  ["2020-01-01", 1, "Устарели"],
])("classifies observation %s with %d rows honestly", (date, rows, label) => {
  expect(stockAge(date, rows).label).toBe(label);
});
it("never infers FBS from a missing or incompatible type", () => {
  expect(warehouseType({ is_rfbs: null, warehouse_type: "" })).toBe(
    "Неизвестен",
  );
  expect(warehouseType({ is_rfbs: false, warehouse_type: "unknown" })).toBe(
    "Неизвестен",
  );
});
