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
import { AutomaticStockControls } from "./AutomaticStockControls";

const scope = {
  tenant: "cross-e2e",
  moysklad_account: "source",
  ozon_account: "market",
};
const target = {
  ozon_product_id: "101",
  ozon_warehouse_id: "201",
  source_physical: "5",
  source_reserve: "0",
  marketplace_free: 0,
};
const clients: QueryClient[] = [];
afterEach(() => {
  cleanup();
  for (const client of clients) client.clear();
  clients.length = 0;
  vi.restoreAllMocks();
});
function setup(initial: object = { available: true }) {
  let view = initial;
  const mutations: { path: string; body: Record<string, unknown> }[] = [];
  vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
    const path = String(input);
    if (init?.method === "POST") {
      const body = JSON.parse(String(init.body)) as Record<string, unknown>;
      mutations.push({ path, body });
      if (path.endsWith("/automatic"))
        view = {
          available: true,
          policy: {
            enabled: body.enabled,
            interval_seconds: 60,
            valid_until: new Date(Date.now() + 3600000).toISOString(),
            targets: [],
          },
        };
    }
    return new Response(JSON.stringify(view), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  clients.push(client);
  render(
    <QueryClientProvider client={client}>
      <AutomaticStockControls
        scope={scope}
        targets={[target]}
        onEnabledChange={() => {}}
      />
    </QueryClientProvider>,
  );
  return mutations;
}

it("requires an explicit scoped test policy and records its quantities", async () => {
  const calls = setup();
  fireEvent.click(
    await screen.findByRole("button", {
      name: "Настроить автоматическую синхронизацию",
    }),
  );
  const dialog = screen.getByRole("dialog", {
    name: "Настроить автоматическую синхронизацию?",
  });
  expect(within(dialog).getByText(/утверждения оператора/)).toBeTruthy();
  const submit = within(dialog).getByRole("button", {
    name: "Включить автоматическую проверку",
  });
  expect((submit as HTMLButtonElement).disabled).toBe(true);
  fireEvent.change(within(dialog).getByLabelText("Страховой остаток"), {
    target: { value: "1" },
  });
  fireEvent.change(within(dialog).getByLabelText("Максимум к отправке"), {
    target: { value: "5" },
  });
  fireEvent.click(within(dialog).getByRole("checkbox"));
  fireEvent.click(submit);
  await waitFor(() => expect(calls).toHaveLength(1));
  expect(calls[0].body).toMatchObject({
    ...scope,
    enabled: true,
    acknowledge: true,
    ozon_product_id: "101",
    ozon_warehouse_id: "201",
    safety_stock: "1",
    unreflected: "0",
    unseen_bound: "0",
    maximum: 5,
  });
  expect(typeof calls[0].body.idempotency_key).toBe("string");
  await screen.findByRole("button", {
    name: "Остановить автоматическую проверку",
  });
});

it("shows an unknown outcome and supports pausing without claiming synchronization", async () => {
  const calls = setup({
    available: true,
    policy: {
      enabled: true,
      interval_seconds: 60,
      valid_until: new Date(Date.now() + 3600000).toISOString(),
      targets: [],
    },
    last_cycle: {
      id: "cycle",
      state: "BLOCKED",
      outcome: "PUBLICATION_UNKNOWN",
      started_at: "2026-10-29T13:00:00Z",
      metrics: { source_physical: "5", candidate: "5", marketplace_free: 0 },
    },
  });
  expect(await screen.findByText(/Ответ Ozon не подтверждён/)).toBeTruthy();
  expect(screen.getByText("Требуется внимание")).toBeTruthy();
  fireEvent.click(
    screen.getByRole("button", { name: "Остановить автоматическую проверку" }),
  );
  await waitFor(() => expect(calls[0].body.enabled).toBe(false));
});

it("does not expose test controls to other accounts", async () => {
  setup({ available: false });
  await waitFor(() => expect(globalThis.fetch).toHaveBeenCalled());
  expect(
    screen.queryByRole("region", { name: "Автоматическая синхронизация" }),
  ).toBeNull();
});
