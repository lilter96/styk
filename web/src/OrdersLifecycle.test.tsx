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
import { OrdersConfig } from "./OrdersConfig";
import { OrdersRealizationReport } from "./OrdersRealizationReport";
import {
  emptyLifecycle,
  lifecycleConsentRequired,
  lifecycleValidation,
} from "./OrdersLifecycleTypes";
import { orderScopeBody, type OrdersConfigData } from "./OrdersTypes";

const scope = { tenant: "lifecycle", moy: "moy", ozon: "ozon" };
const before = "11111111-1111-4111-8111-111111111111";
const after = "22222222-2222-4222-8222-222222222222";
const config: OrdersConfigData = {
  scope: orderScopeBody(scope),
  revision: 1,
  enabled: true,
  test_enabled: true,
  read_configured: true,
  read_enabled: true,
  publication_available: true,
  interval_seconds: 300,
  source_freshness_seconds: 300,
  preview_ttl_seconds: 300,
  pii_enabled: false,
  pii_retention_seconds: 0,
  organization_id: "org",
  agent_id: "agent",
  store_id: "store",
  state_id: before,
  sales_channel_id: "channel",
  currency_id: "currency",
  field_mappings: [{ source: "posting_number", target: "name" }],
  status_mappings: [],
  automation: { perpetual: true, actions: ["mirror_create"], fields: ["name"] },
};
const policy = {
  ...emptyLifecycle(),
  enabled: true,
  mappings: [
    {
      state_id: after,
      from_state_ids: [before],
      action: "posting_pack",
      scheme: "FBS" as const,
      event_kind: "assembly_ready",
      whole_posting: true,
    },
  ],
};
const json = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), { status });

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

function mount(value = config, unavailable = false) {
  const requests: Record<string, unknown>[] = [];
  const saved = vi.fn();
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, init?: RequestInit) => {
      const url = new URL(path, "http://localhost");
      if (url.pathname.endsWith("/config") && init?.method === "POST") {
        const body = JSON.parse(init.body as string);
        requests.push(body);
        return json({ ...body.config, revision: 2 });
      }
      if (url.pathname.endsWith("/merchant-intents"))
        return json({ intents: [] });
      if (url.pathname.endsWith("/metadata/registry"))
        return json({ scope: value.scope, revision: 0, attributes: [] });
      if (url.pathname.endsWith("/metadata"))
        return unavailable
          ? json({}, 503)
          : json({
              scope: value.scope,
              organizations: [{ id: "org", name: "Организация" }],
              agents: [{ id: "agent", name: "Ozon" }],
              states: [
                { id: before, name: "Новый" },
                { id: after, name: "Собран" },
              ],
              sales_channels: [{ id: "channel", name: "Ozon" }],
              currencies: [{ id: "currency", iso_code: "RUB" }],
              stores: [],
              attributes: [],
            });
      throw new Error("unexpected lifecycle test endpoint");
    }),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <OrdersConfig
          scope={scope}
          config={value}
          section="lifecycle"
          onSaved={saved}
          onDirty={() => {}}
        />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return { requests, saved };
}

it("requires a distinct owner opt-in and exact transition while preserving the mirror grant", async () => {
  const { requests } = mount();
  const save = screen.getByRole("button", { name: "Сохранить настройки" });
  fireEvent.click(
    screen.getByRole("checkbox", {
      name: /Выполнять команды из МойСклад автоматически/,
    }),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Добавить переход МойСклад" }),
  );
  await screen.findByRole("option", { name: "Собран" });
  fireEvent.change(screen.getByLabelText("Новое состояние МойСклад 1"), {
    target: { value: after },
  });
  fireEvent.click(screen.getByLabelText("Из состояния «Новый», переход 1"));
  fireEvent.click(
    screen.getByLabelText("Событие относится ко всем позициям отправления"),
  );
  expect((save as HTMLButtonElement).disabled).toBe(true);
  fireEvent.click(screen.getByLabelText(/Разрешаю выбранные команды Ozon/));
  expect((save as HTMLButtonElement).disabled).toBe(false);
  fireEvent.click(save);
  await waitFor(() => expect(requests).toHaveLength(1));
  expect(requests[0].lifecycle_consent).toBe(true);
  expect(requests[0].automation_consent).toBe(false);
  expect((requests[0].config as OrdersConfigData).automation).toEqual(
    config.automation,
  );
  expect((requests[0].config as OrdersConfigData).lifecycle).toEqual(policy);
});

it("allows lifecycle revocation without provider metadata or renewed consent", async () => {
  const { requests } = mount({ ...config, lifecycle: policy }, true);
  fireEvent.click(
    screen.getByRole("checkbox", {
      name: /Выполнять команды из МойСклад автоматически/,
    }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Сохранить настройки" }));
  await waitFor(() => expect(requests).toHaveLength(1));
  expect(requests[0].lifecycle_consent).toBe(false);
  expect((requests[0].config as OrdersConfigData).lifecycle?.enabled).toBe(
    false,
  );
  expect((requests[0].config as OrdersConfigData).automation).toEqual(
    config.automation,
  );
});

it("does not expand consent through unrelated settings and rejects unsafe combinations", () => {
  expect(lifecycleConsentRequired(policy, { ...policy, revision: 9 })).toBe(
    false,
  );
  expect(
    lifecycleConsentRequired(policy, { ...policy, auto_demand: true }),
  ).toBe(true);
  expect(lifecycleValidation({ ...config, lifecycle: policy })).toBe("");
  expect(
    lifecycleValidation({
      ...config,
      lifecycle: {
        ...policy,
        auto_demand: true,
        demand_moment: "ON_OZON_ACCEPTANCE_CONFIRMED",
      },
    }),
  ).toMatch(/недоступна/);
  expect(
    lifecycleValidation({
      ...config,
      status_mappings: [
        {
          status: "awaiting_deliver",
          substatus: { presence: "missing" },
          state_id: after,
        },
      ],
      lifecycle: policy,
    }),
  ).toMatch(/не должно/);
  expect(
    lifecycleValidation({
      ...config,
      lifecycle: {
        ...policy,
        mappings: [{ ...policy.mappings[0], whole_posting: false }],
      },
    }),
  ).toMatch(/всех позиций/);
});

it("allows one physical handoff to queue distinct rFBS delivery and demand actions and rejects conflicting event meaning", () => {
  const handoff = {
    state_id: after,
    from_state_ids: [before],
    action: "posting_ship",
    scheme: "rFBS" as const,
    event_kind: "handoff_confirmed",
    whole_posting: true,
  };
  const value: OrdersConfigData = {
    ...config,
    native_documents: {
      enabled: true,
      allow_posted: false,
      actions: ["demand_create"],
      maximum_quantity: "10",
      event_max_age_seconds: 300,
      attested_at: "",
      valid_until: "",
    },
    lifecycle: {
      ...policy,
      auto_demand: true,
      mappings: [handoff, { ...handoff, action: "demand_create" }],
    },
  };
  expect(lifecycleValidation(value)).toBe("");
  expect(
    lifecycleValidation({
      ...value,
      lifecycle: {
        ...value.lifecycle!,
        mappings: [
          { ...handoff, action: "posting_pack", event_kind: "assembly_ready" },
          { ...handoff, action: "demand_create" },
        ],
      },
    }),
  ).toMatch(/разные физические события/);
});

it("reads a monthly report only on request and retains exact money strings", async () => {
  const calls: string[] = [];
  const field = (value: string) => ({
    presence: value === "0" ? "zero" : "value",
    value,
  });
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string) => {
      calls.push(path);
      const url = new URL(path, "http://localhost");
      return json({
        scope: config.scope,
        period: {
          year: Number(url.searchParams.get("year")),
          month: Number(url.searchParams.get("month")),
        },
        number: "REPORT-1",
        currency: "RUB",
        observed_at: "2026-11-06T12:00:00Z",
        rows: [
          {
            number: 1,
            sku: field("9007199254740993"),
            offer_id: field("SKU"),
            delivery: {
              quantity: field("1"),
              amount: field("9007199254740993.01"),
              total: field("-0.01"),
            },
            return: {
              quantity: field("0"),
              amount: { presence: "missing" },
              total: field("0"),
            },
          },
        ],
        blockers: [{ code: "accounting_mapping_review_required" }],
      });
    }),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  render(
    <QueryClientProvider client={client}>
      <OrdersRealizationReport scope={scope} />
    </QueryClientProvider>,
  );
  expect(calls).toHaveLength(0);
  fireEvent.change(screen.getByLabelText("Месяц отчёта"), {
    target: { value: "2026-09" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Прочитать отчёт Ozon" }));
  await screen.findByText("REPORT-1");
  expect(calls).toHaveLength(1);
  expect(calls[0]).toContain("year=2026&month=9");
  expect(screen.getByText(/9007199254740993.01/)).toBeTruthy();
  expect(screen.getByText(/Финансовая строка возврата/)).toBeTruthy();
  expect(screen.queryByRole("button", { name: /Импортировать/ })).toBeNull();
});
