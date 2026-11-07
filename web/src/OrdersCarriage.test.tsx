import { afterEach, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { OrdersCarriage } from "./OrdersCarriage";
import type {
  CarriageAction,
  CarriagePlan,
  CarriageWork,
} from "./OrdersCarriageTypes";
import { orderScopeBody, type OrdersConfigData } from "./OrdersTypes";
const scope = { tenant: "carriage-test", moy: "moy", ozon: "ozon" };
const config: OrdersConfigData = {
  scope: orderScopeBody(scope),
  revision: 7,
  enabled: true,
  test_enabled: true,
  read_enabled: true,
  interval_seconds: 300,
  source_freshness_seconds: 300,
  preview_ttl_seconds: 300,
  pii_enabled: false,
  pii_retention_seconds: 0,
  organization_id: "org",
  agent_id: "agent",
  store_id: "store",
  state_id: "state",
  sales_channel_id: "channel",
  currency_id: "currency",
  field_mappings: [],
  status_mappings: [],
};
const plan = (action: CarriageAction = "CREATE"): CarriagePlan => ({
  id: `plan-${action}`,
  scope: config.scope,
  action,
  config_revision: 7,
  credentials: { ozon: 1, moysklad: 1 },
  delivery_method_id: "9007199254740993",
  warehouse_id: "25",
  ...(action === "APPROVE" ? { carriage_id: "123" } : {}),
  members: [
    {
      posting_number: "SYNTHETIC-A",
      status: "awaiting_deliver",
      ready: true,
      lines: [
        {
          identity: { sku: "9007199254740993", offer_id: "SYNTHETIC-SKU" },
          quantity: "1",
          name: { presence: "missing" },
          price: { presence: "missing" },
        },
      ],
      blockers: [],
    },
    {
      posting_number: "SYNTHETIC-B",
      status: "awaiting_deliver",
      ready: true,
      lines: [
        {
          identity: { sku: "9007199254740993", offer_id: "SYNTHETIC-SKU" },
          quantity: "1",
          name: { presence: "missing" },
          price: { presence: "missing" },
        },
      ],
      blockers: [],
    },
  ],
  membership_fingerprint: "f".repeat(64),
  fingerprint: "b".repeat(64),
  created_at: new Date().toISOString(),
  expires_at: new Date(Date.now() + 300000).toISOString(),
});
const work = (p = plan(), state = "QUEUED"): CarriageWork => ({
  id: `work-${p.action}`,
  scope: config.scope,
  plan: p,
  preview_id: p.id,
  config_revision: 7,
  state,
  dispatch_count: 0,
  created_at: new Date().toISOString(),
  updated_at: new Date().toISOString(),
});
const json = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), { status });
const clients: QueryClient[] = [];
afterEach(() => {
  cleanup();
  clients.forEach((c) => c.clear());
  clients.length = 0;
  sessionStorage.clear();
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
      <OrdersCarriage scope={scope} config={config} />
    </QueryClientProvider>,
  );
}
async function inputs() {
  await screen.findByText(
    "Операций с партиями пока нет. Начните с проверки состава.",
  );
  fireEvent.change(
    screen.getByLabelText("Идентификатор способа доставки Ozon"),
    { target: { value: "9007199254740993" } },
  );
  fireEvent.change(
    screen.getByLabelText("Все товары прослеживаемые в Беларуси"),
    { target: { value: "false" } },
  );
}
function consent() {
  fireEvent.click(
    screen.getByLabelText(
      /Разрешаю создать всю проверенную партию|Разрешаю подтвердить именно эту созданную/,
    ),
  );
  fireEvent.click(
    screen.getByLabelText(
      /Подтверждаю фактическую готовность всех отправлений/,
    ),
  );
}
it("requires exact full batch and separate readiness consents for CREATE and APPROVE", async () => {
  const approvals: Record<string, unknown>[] = [];
  const previews: Record<string, unknown>[] = [];
  let history: CarriageWork[] = [];
  let current = plan();
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, init?: RequestInit) => {
      if (path.includes("/operations")) return json({ operations: history });
      const body = JSON.parse(init?.body as string);
      if (path.endsWith("/preview")) {
        previews.push(body);
        current = plan(body.action);
        return json({ id: current.id, plan: current, validation: [] });
      }
      if (path.endsWith("/approve")) {
        approvals.push(body);
        history = [work(current)];
        return json(history[0]);
      }
      throw new Error("unexpected carriage call");
    }),
  );
  mount();
  await inputs();
  fireEvent.click(
    screen.getByRole("button", { name: "Проверить полную партию" }),
  );
  await screen.findByRole("heading", { name: "Создание всей готовой партии" });
  expect(approvals).toHaveLength(0);
  const create = screen.getByRole("button", { name: "Создать партию в Ozon" });
  expect((create as HTMLButtonElement).disabled).toBe(true);
  fireEvent.click(
    screen.getByLabelText(/Разрешаю создать всю проверенную партию/),
  );
  expect((create as HTMLButtonElement).disabled).toBe(true);
  fireEvent.click(
    screen.getByLabelText(
      /Подтверждаю фактическую готовность всех отправлений/,
    ),
  );
  fireEvent.click(create);
  await waitFor(() => expect(approvals).toHaveLength(1));
  expect(previews[0]).toMatchObject({
    action: "CREATE",
    delivery_method_id: "9007199254740993",
    all_blr_traceable: false,
    expected_revision: 7,
  });
  await screen.findByText("Операция в очереди");
  fireEvent.change(screen.getByLabelText("Операция партии"), {
    target: { value: "APPROVE" },
  });
  fireEvent.change(
    screen.getByLabelText("Идентификатор созданной партии Ozon"),
    { target: { value: "123" } },
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Проверить созданную партию" }),
  );
  await screen.findByRole("heading", {
    name: "Подтверждение созданной партии",
  });
  expect(
    (
      screen.getByLabelText(
        /Подтверждаю фактическую готовность всех отправлений/,
      ) as HTMLInputElement
    ).checked,
  ).toBe(false);
  consent();
  fireEvent.click(
    screen.getByRole("button", { name: "Подтвердить партию в Ozon" }),
  );
  await waitFor(() => expect(approvals).toHaveLength(2));
  expect(previews[1]).toMatchObject({ action: "APPROVE", carriage_id: "123" });
  expect(previews[1]).not.toHaveProperty("all_blr_traceable");
  expect(
    approvals.every(
      (value) =>
        value.acknowledge &&
        value.all_ready_consent &&
        value.physical_readiness_confirmed,
    ),
  ).toBe(true);
});
it.each(["changed", "expired", "foreign"])(
  "blocks %s batch before any approval",
  async (mode) => {
    let approvals = 0;
    vi.stubGlobal(
      "fetch",
      vi.fn(async (path: string) => {
        if (path.includes("/operations")) return json({ operations: [] });
        if (path.endsWith("/approve")) {
          approvals++;
          return json({});
        }
        const p = plan();
        if (mode === "expired") p.expires_at = "2020-01-01T00:00:00Z";
        if (mode === "foreign") p.scope = { ...p.scope, tenant: "foreign" };
        return json({
          id: p.id,
          plan: p,
          validation:
            mode === "changed" ? [{ code: "CARRIAGE_MEMBERSHIP_CHANGED" }] : [],
        });
      }),
    );
    mount();
    await inputs();
    fireEvent.click(
      screen.getByRole("button", { name: "Проверить полную партию" }),
    );
    if (mode === "foreign") await screen.findByRole("alert");
    else
      await screen.findByRole("heading", {
        name: "Создание всей готовой партии",
      });
    expect(
      screen.queryByRole("button", { name: "Создать партию в Ozon" }),
    ).toBeNull();
    expect(approvals).toBe(0);
    if (mode !== "foreign") {
      fireEvent.click(
        screen.getByRole("button", { name: "Закрыть проверку без записи" }),
      );
      expect(
        screen.queryByRole("region", { name: "Проверка партии Ozon" }),
      ).toBeNull();
      expect(approvals).toBe(0);
    }
  },
);
it("keeps lost acknowledgement fenced across reload and re-reads without another write", async () => {
  let approvals = 0;
  let rereads = 0;
  let history: CarriageWork[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string) => {
      if (path.includes("/operations")) return json({ operations: history });
      if (path.endsWith("/preview"))
        return json({ id: plan().id, plan: plan(), validation: [] });
      if (path.endsWith("/approve")) {
        approvals++;
        throw new TypeError("lost ACK");
      }
      if (path.endsWith("/reread")) {
        rereads++;
        return json({ queued: true });
      }
      throw new Error("unexpected carriage call");
    }),
  );
  const first = mount();
  await inputs();
  fireEvent.click(
    screen.getByRole("button", { name: "Проверить полную партию" }),
  );
  await screen.findByRole("heading", { name: "Создание всей готовой партии" });
  consent();
  fireEvent.click(
    screen.getByRole("button", { name: "Создать партию в Ozon" }),
  );
  await screen.findByText(/Ответ не получен/);
  first.unmount();
  mount();
  await screen.findByText(/Результат подтверждения пока неизвестен/);
  expect(
    (
      screen.getByRole("button", {
        name: "Проверить полную партию",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  await screen.findByText(
    "Операций с партиями пока нет. Начните с проверки состава.",
  );
  history = [work(plan(), "UNKNOWN")];
  fireEvent.click(
    screen.getByRole("button", { name: "Обновить историю партий" }),
  );
  await screen.findByText("Результат неизвестен — только перечитывание");
  fireEvent.click(
    screen.getByRole("button", { name: "Перечитать результат партии" }),
  );
  await waitFor(() => expect(rereads).toBe(1));
  expect(approvals).toBe(1);
  expect(screen.queryByText("Подтверждение партии сверено")).toBeNull();
});
