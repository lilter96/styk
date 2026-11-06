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
import { OrdersSettingsPage } from "./OrdersSettingsPage";
import { orderScopeBody, type OrdersConfigData } from "./OrdersTypes";

const scope = { tenant: "settings-test", moy: "moy", ozon: "ozon" };
const config: OrdersConfigData = {
  scope: orderScopeBody(scope),
  revision: 1,
  enabled: true,
  test_enabled: true,
  publication_available: true,
  interval_seconds: 300,
  source_freshness_seconds: 300,
  preview_ttl_seconds: 300,
  pii_enabled: false,
  pii_retention_seconds: 0,
  organization_id: "org",
  agent_id: "agent",
  store_id: "legacy",
  state_id: "state",
  sales_channel_id: "channel",
  currency_id: "currency",
  field_mappings: [{ source: "posting_number", target: "name" }],
  status_mappings: [],
  automation: {
    perpetual: true,
    actions: ["mirror_create"],
    fields: ["name", "state"],
  },
};
const json = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), { status });
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
function mount(
  handler?: (url: URL, init?: RequestInit) => Response | undefined,
) {
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    const url = new URL(path, "http://localhost");
    const response = handler?.(url, init);
    if (response) return response;
    if (url.pathname.endsWith("/config")) return json(config);
    if (url.pathname.endsWith("/warehouses"))
      return json({ scope: config.scope, confirmed: 0, total: 0, items: [] });
    if (url.pathname.endsWith("/metadata/registry"))
      return json({ scope: config.scope, revision: 0, attributes: [] });
    if (url.pathname.endsWith("/metadata"))
      return json({
        scope: config.scope,
        organizations: [{ id: "org", name: "Организация" }],
        agents: [{ id: "agent", name: "Контрагент" }],
        states: [{ id: "state", name: "Новый" }],
        sales_channels: [{ id: "channel", name: "Ozon" }],
        currencies: [{ id: "currency", iso_code: "RUB" }],
        stores: [],
        attributes: [],
      });
    throw new Error("unexpected settings endpoint");
  });
  vi.stubGlobal("fetch", fetcher);
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  const content = (current = scope) => (
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <OrdersSettingsPage key={JSON.stringify(current)} scope={current} />
      </MemoryRouter>
    </QueryClientProvider>
  );
  const mounted = render(content());
  return {
    ...mounted,
    fetcher,
    changeScope: (next: typeof scope) => mounted.rerender(content(next)),
  };
}

it("keeps the draft and permanent policy through sections, confirms cancel and preserves legacy fields", async () => {
  const saved: Record<string, unknown>[] = [];
  mount((url, init) =>
    url.pathname.endsWith("/config") && init?.method === "POST"
      ? (saved.push(JSON.parse(init.body as string)),
        json({ ...config, revision: 2 }))
      : undefined,
  );
  await screen.findByRole("heading", { name: "Как работают ваши заказы" });
  fireEvent.click(screen.getByRole("button", { name: "Дополнительно" }));
  fireEvent.change(screen.getByLabelText("Интервал проверки, секунд"), {
    target: { value: "120" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Куда передавать" }));
  await screen.findByText(/Склады ещё не загружены/);
  fireEvent.click(screen.getByRole("button", { name: "Дополнительно" }));
  expect(
    (screen.getByLabelText("Интервал проверки, секунд") as HTMLInputElement)
      .value,
  ).toBe("120");
  vi.spyOn(window, "confirm").mockReturnValue(false);
  fireEvent.click(screen.getByRole("button", { name: "Отменить" }));
  expect(screen.getByText("Есть несохранённые изменения")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Сохранить настройки" }));
  await waitFor(() => expect(saved).toHaveLength(1));
  expect(saved[0].automation_consent).toBe(false);
  expect(saved[0].config).toMatchObject({
    store_id: "legacy",
    automation: {
      perpetual: true,
      actions: ["mirror_create"],
      fields: ["name", "state"],
    },
    interval_seconds: 120,
  });
});

it("retains the draft on a revision conflict and reuses the request key on retry", async () => {
  const requests: Record<string, unknown>[] = [];
  mount((url, init) =>
    url.pathname.endsWith("/config") && init?.method === "POST"
      ? (requests.push(JSON.parse(init.body as string)),
        json({ error: "ORDER_EVIDENCE_CHANGED" }, 409))
      : undefined,
  );
  await screen.findByRole("heading", { name: "Как работают ваши заказы" });
  fireEvent.click(
    screen.getByRole("checkbox", { name: "Получать отправления из Ozon" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Сохранить настройки" }));
  await screen.findByRole("alert");
  expect(screen.getByText("Есть несохранённые изменения")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Сохранить настройки" }));
  await waitFor(() => expect(requests).toHaveLength(2));
  expect(requests[0].idempotency_key).toBe(requests[1].idempotency_key);
});

it("rejects a mismatched account response and never shows the prior account configuration", async () => {
  const f = mount();
  await screen.findByRole("heading", { name: "Как работают ваши заказы" });
  f.changeScope({ ...scope, ozon: "another" });
  await screen.findByRole("alert");
  expect(screen.queryByLabelText("Организация")).toBeNull();
  expect(screen.queryByText("Есть несохранённые изменения")).toBeNull();
});

it("allows revoking automation when provider metadata is unavailable", async () => {
  const requests: Record<string, unknown>[] = [];
  mount((url, init) =>
    url.pathname.endsWith("/metadata")
      ? json({ error: "UNAVAILABLE" }, 503)
      : url.pathname.endsWith("/config") && init?.method === "POST"
        ? (requests.push(JSON.parse(init.body as string)),
          json({ ...config, automation: null, revision: 2 }))
        : undefined,
  );
  await screen.findByRole("heading", { name: "Как работают ваши заказы" });
  fireEvent.click(screen.getByRole("button", { name: "Автоматизация" }));
  fireEvent.click(
    screen.getByRole("button", { name: "Отключить автоматизацию" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Сохранить настройки" }));
  await waitFor(() => expect(requests).toHaveLength(1));
  expect((requests[0].config as OrdersConfigData).automation).toBeNull();
});

it("keeps receiving postings enabled when document transmission is turned off", async () => {
  mount();
  await screen.findByRole("heading", { name: "Как работают ваши заказы" });
  fireEvent.click(
    screen.getByRole("checkbox", { name: "Передавать заказы в МойСклад" }),
  );
  expect(
    (
      screen.getByRole("checkbox", {
        name: "Получать отправления из Ozon",
      }) as HTMLInputElement
    ).checked,
  ).toBe(true);
});
