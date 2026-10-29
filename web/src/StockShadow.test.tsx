import { afterEach, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { StockShadow } from "./StockShadow";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

it("shows actual zero and missing evidence without sending a write", async () => {
  const fetcher = vi.spyOn(globalThis, "fetch").mockResolvedValue(
    new Response(
      JSON.stringify({
        state: "DISABLED",
        checks: [],
        targets: [
          {
            product_id: "101",
            warehouse_id: "201",
            source_item_id: "item",
            source_warehouse_id: "store",
            physical: "0",
            reserve: null,
            candidate: null,
            marketplace_free: "2",
            unreflected: null,
            unseen_bound: null,
            safety_stock: null,
            source_batch_finished: null,
            marketplace_batch_finished: null,
            source_batch_started: null,
            marketplace_batch_started: null,
            checks: [
              { code: "SOURCE_RESERVE_MISSING", passed: false, message: "" },
            ],
          },
        ],
      }),
      { status: 200, headers: { "Content-Type": "application/json" } },
    ),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <StockShadow tenant="tenant" moy="moy" ozon="ozon" />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  fireEvent.click(screen.getByRole("button", { name: "Проверить остатки" }));
  const dialog = await screen.findByRole("dialog", {
    name: "Проверка остатков",
  });
  expect(
    await within(dialog).findByText("Нет данных о резерве МойСклад"),
  ).toBeTruthy();
  expect(within(dialog).getByText("0")).toBeTruthy();
  expect(
    within(dialog)
      .getByRole("link", { name: "Сопоставить товары" })
      .getAttribute("href"),
  ).toBe("/products");
  expect(within(dialog).queryByText("Разница:", { exact: false })).toBeNull();
  expect(fetcher).toHaveBeenCalledTimes(1);
  expect(fetcher.mock.calls[0][0]).toContain("/api/stock-sync/shadow?");
  expect(fetcher.mock.calls[0][1]?.method || "GET").toBe("GET");
  client.clear();
});
