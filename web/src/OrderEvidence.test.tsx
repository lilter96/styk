import { afterEach, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { OrderEvidence } from "./OrderEvidence";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});
it("shows observed reserve without treating missing order coverage as zero", async () => {
  const calls: { method: string; body?: string }[] = [];
  vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
    calls.push({ method: init?.method || "GET", body: init?.body?.toString() });
    const response =
      init?.method === "POST"
        ? { source_status: "LIMIT_REACHED", posting_status: "COMPLETED" }
        : {
            source_batch_id: 1,
            source_from: "2026-10-29T10:00:00Z",
            source_to: "2026-10-29T11:00:00Z",
            source_checked_at: "2026-10-29T11:00:01Z",
            positions: [
              {
                order_id: "synthetic-order",
                position_id: "p1",
                quantity: "3",
                reserve: null,
                shipped: "0",
                applicable: true,
                observed_at: "2026-10-29T11:00:01Z",
              },
            ],
            postings: [],
            truncated: false,
          };
    return new Response(JSON.stringify(response), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <OrderEvidence
        scope={{ tenant: "t", moysklad_account: "m", ozon_account: "o" }}
        product="1"
        warehouse="2"
      />
    </QueryClientProvider>,
  );
  fireEvent.click(screen.getByText("Заказы и резервы"));
  expect(await screen.findByText(/резерв неизвестно/)).toBeTruthy();
  expect(
    screen.getByText(/не доказывает отсутствие обязательств/),
  ).toBeTruthy();
  fireEvent.change(screen.getByLabelText("Период проверки"), {
    target: { value: "24" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Проверить заказы" }));
  await screen.findByText(/неполные данные не сохранены/);
  await waitFor(() =>
    expect(
      calls.some(
        (c) =>
          c.method === "POST" && JSON.parse(c.body || "{}").window_hours === 24,
      ),
    ).toBe(true),
  );
  client.clear();
});
