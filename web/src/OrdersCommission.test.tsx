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
import { OrdersCommissionImport } from "./OrdersCommissionImport";
import {
  emptyCommissionPolicy,
  financeConsentRequired,
  financeValidation,
  type CommissionPlan,
  type CommissionPolicy,
  type CommissionPreview,
  type CommissionWork,
} from "./OrdersFinanceTypes";
import { orderScopeBody, type OrdersConfigData } from "./OrdersTypes";

const scope = { tenant: "commission", moy: "moy", ozon: "ozon" };
const id = (value: number) =>
  `00000000-0000-4000-8000-${String(value).padStart(12, "0")}`;
const known = <T,>(value: T) => ({ presence: "value" as const, value });
const missing = { presence: "missing" as const };
const policy: CommissionPolicy = {
  ...emptyCommissionPolicy(),
  enabled: true,
  organization_id: id(1),
  agent_id: id(2),
  currency_id: id(3),
  currency_iso: "RUB",
  contract_id: id(4),
  rate_value: "1",
  price_basis: "SELLER_PRICE",
  vat_enabled: known(false),
  vat_included: known(false),
  vat: "0",
};
const config: OrdersConfigData = {
  scope: orderScopeBody(scope),
  revision: 1,
  enabled: true,
  test_enabled: true,
  read_enabled: true,
  read_configured: true,
  publication_available: true,
  interval_seconds: 300,
  source_freshness_seconds: 300,
  preview_ttl_seconds: 300,
  pii_enabled: false,
  pii_retention_seconds: 0,
  organization_id: id(1),
  agent_id: id(2),
  store_id: id(7),
  state_id: id(5),
  sales_channel_id: id(6),
  currency_id: id(3),
  field_mappings: [{ source: "posting_number", target: "name" }],
  status_mappings: [],
  automation: { perpetual: true, actions: ["mirror_create"], fields: ["name"] },
};
const plan = (): CommissionPlan => ({
  id: "plan-1",
  scope: config.scope,
  config_revision: 1,
  policy_revision: 1,
  policy,
  source: {
    scope: config.scope,
    period: { year: 2026, month: 10 },
    number: "SOURCE-REPORT",
    currency: "RUB",
    observed_at: "2026-11-07T00:00:00Z",
    fingerprint: "f".repeat(64),
    rows: [],
    blockers: [],
  },
  external_code: "styk-commission-test",
  lines: [
    {
      source_row: 1,
      mapping: {
        id: "mapping",
        version: 1,
        moysklad_kind: "product",
        moysklad_id: id(8),
      },
      quantity: "1",
      price: { amount: "100.01", currency: "RUB" },
      reward: { amount: "10.01", currency: "RUB" },
    },
  ],
  totals: { currency: "RUB", gross: "100.01", reward: "10.01", net: "90" },
  fingerprint: "p".repeat(64),
  created_at: new Date().toISOString(),
  expires_at: new Date(Date.now() + 300000).toISOString(),
});
const work = (state = "ACKNOWLEDGED", dispatch_count = 1): CommissionWork => ({
  id: "work-1",
  scope: config.scope,
  plan: plan(),
  preview_id: "plan-1",
  config_revision: 1,
  state,
  code: "FINANCE_TEST",
  receipt: { state: "ACKNOWLEDGED", external_id: id(9) },
  dispatch_count,
  automatic: false,
  created_at: new Date().toISOString(),
  updated_at: new Date().toISOString(),
});
const json = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), { status });
const clients: QueryClient[] = [];
afterEach(() => {
  cleanup();
  clients.forEach((client) => client.clear());
  clients.length = 0;
  sessionStorage.clear();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
function provider(node: React.ReactNode) {
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false },
    },
  });
  clients.push(client);
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter>{node}</MemoryRouter>
    </QueryClientProvider>,
  );
}

it("requires explicit accountant choices and separate finance consent without expanding mirror or lifecycle grants", async () => {
  const saves: Record<string, unknown>[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, init?: RequestInit) => {
      const url = new URL(path, "http://localhost");
      if (url.pathname.endsWith("finance/metadata"))
        return json({
          scope: config.scope,
          contracts: [
            {
              id: id(4),
              name: "Комиссионный договор Ozon",
              organization_id: id(1),
              agent_id: id(2),
              currency_id: id(3),
              contract_type: "Commission",
              reward_type: "None",
              archived: false,
            },
          ],
          truncated: false,
        });
      if (url.pathname.endsWith("metadata/registry"))
        return json({ scope: config.scope, revision: 0, attributes: [] });
      if (url.pathname.endsWith("metadata"))
        return json({
          scope: config.scope,
          organizations: [{ id: id(1), name: "Организация" }],
          agents: [{ id: id(2), name: "Ozon" }],
          states: [{ id: id(5), name: "Новый" }],
          sales_channels: [{ id: id(6), name: "Канал" }],
          currencies: [{ id: id(3), iso_code: "RUB" }],
          stores: [],
          attributes: [],
        });
      if (url.pathname.endsWith("config") && init?.method === "POST") {
        const body = JSON.parse(init.body as string);
        saves.push(body);
        return json({ ...body.config, revision: 2 });
      }
      throw new Error("unexpected finance configuration request");
    }),
  );
  provider(
    <OrdersConfig
      scope={scope}
      config={config}
      section="lifecycle"
      onSaved={() => {}}
      onDirty={() => {}}
    />,
  );
  fireEvent.click(
    screen.getByText("Черновики отчётов комиссионера", { selector: "summary" }),
  );
  fireEvent.click(
    screen.getByLabelText("Разрешить черновики отчётов комиссионера"),
  );
  expect(
    (
      screen.getByLabelText(
        "Курс валюты финансового отчёта",
      ) as HTMLInputElement
    ).value,
  ).toBe("");
  await waitFor(() =>
    expect(
      (
        screen.getByRole("combobox", {
          name: "Организация финансового отчёта",
        }) as HTMLSelectElement
      ).disabled,
    ).toBe(false),
  );
  for (const [label, value] of [
    ["Организация финансового отчёта", id(1)],
    ["Контрагент финансового отчёта", id(2)],
    ["Валюта финансового отчёта", id(3)],
  ])
    fireEvent.change(screen.getByRole("combobox", { name: label }), {
      target: { value },
    });
  fireEvent.click(
    screen.getByRole("button", { name: "Прочитать комиссионные договоры" }),
  );
  await screen.findByRole("option", { name: "Комиссионный договор Ozon" });
  fireEvent.change(
    screen.getByRole("combobox", { name: "Комиссионный договор" }),
    { target: { value: id(4) } },
  );
  fireEvent.change(screen.getByLabelText("Курс валюты финансового отчёта"), {
    target: { value: "1" },
  });
  for (const [label, value] of [
    ["Основание цены отчёта", "SELLER_PRICE"],
    ["Учёт НДС в отчёте", "false"],
    ["НДС в цене отчёта", "false"],
  ])
    fireEvent.change(screen.getByRole("combobox", { name: label }), {
      target: { value },
    });
  fireEvent.change(screen.getByLabelText("Ставка НДС финансового отчёта, %"), {
    target: { value: "0" },
  });
  expect(
    (
      screen.getByRole("button", {
        name: "Сохранить настройки",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  fireEvent.click(
    screen.getByLabelText(
      /Разрешаю создание непроведённых черновиков отчёта комиссионера/,
    ),
  );
  fireEvent.click(screen.getByRole("button", { name: "Сохранить настройки" }));
  await waitFor(() => expect(saves).toHaveLength(1));
  expect(saves[0]).toMatchObject({
    finance_consent: true,
    lifecycle_consent: false,
    automation_consent: false,
    config: { finance: policy, automation: config.automation },
  });
});

it("does not infer finance policy choices or extend consent through server revisions", () => {
  expect(financeValidation({ ...config, finance: policy })).toBe("");
  expect(
    financeValidation({ ...config, finance: { ...policy, rate_value: "" } }),
  ).toMatch(/курс/);
  expect(
    financeValidation({ ...config, finance: { ...policy, price_basis: "" } }),
  ).toMatch(/основание цены/);
  expect(
    financeValidation({
      ...config,
      finance: { ...policy, vat_enabled: missing },
    }),
  ).toMatch(/Явно/);
  expect(financeConsentRequired(policy, { ...policy, revision: 10 })).toBe(
    false,
  );
  expect(financeConsentRequired(policy, { ...policy, automatic: true })).toBe(
    true,
  );
  expect(
    financeValidation({
      ...config,
      finance: {
        ...policy,
        vat_enabled: known(true),
        vat_included: known(false),
        vat: "20",
      },
    }),
  ).toMatch(/НДС должен быть включён в цену/);
  expect(
    financeValidation({
      ...config,
      finance: {
        ...policy,
        vat_enabled: known(true),
        vat_included: known(true),
        vat: "20",
      },
    }),
  ).toBe("");
});

function mountImport(
  response: CommissionPreview = { id: "plan-1", plan: plan(), validation: [] },
  lost = false,
) {
  const requests: { path: string; body: Record<string, unknown> }[] = [];
  let operations: CommissionWork[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, init?: RequestInit) => {
      const url = new URL(path, "http://localhost");
      if (!init?.method || init.method === "GET") return json({ operations });
      requests.push({
        path: url.pathname,
        body: JSON.parse(init.body as string),
      });
      if (url.pathname.endsWith("preview")) return json(response);
      if (url.pathname.endsWith("approve")) {
        if (lost) throw new TypeError("PRIVATE_LOST_REPLY");
        operations = [work()];
        return json(operations[0]);
      }
      throw new Error("unexpected commission mutation");
    }),
  );
  const rendered = provider(
    <OrdersCommissionImport
      scope={scope}
      config={{ ...config, finance: policy }}
    />,
  );
  return {
    ...rendered,
    requests,
    setOperations: (value: CommissionWork[]) => {
      operations = value;
    },
  };
}
async function previewImport() {
  fireEvent.change(screen.getByLabelText("Месяц черновика комиссионера"), {
    target: { value: "2026-10" },
  });
  fireEvent.click(
    screen.getByRole("button", { name: "Проверить черновик комиссионера" }),
  );
  await screen.findByRole("region", { name: "Проверка финансового черновика" });
}
async function approveImport() {
  fireEvent.click(screen.getByLabelText(/Я проверил полный период, договор/));
  fireEvent.click(
    screen.getByRole("button", { name: "Создать непроведённый черновик" }),
  );
}

it("separately approves one frozen plan and never presents ACK as a verified draft", async () => {
  const { requests } = mountImport();
  await previewImport();
  expect(requests).toHaveLength(1);
  expect(
    (
      screen.getByRole("button", {
        name: "Создать непроведённый черновик",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  await approveImport();
  await screen.findByText("Запрос принят, ждём сверки черновика");
  expect(requests.map((item) => item.path)).toEqual([
    "/api/orders/finance/preview",
    "/api/orders/finance/approve",
  ]);
  expect(requests[1].body).toEqual({
    ...config.scope,
    preview_id: "plan-1",
    idempotency_key: expect.any(String),
    acknowledge: true,
  });
  expect(
    screen.queryByText("Непроведённый черновик создан и сверён"),
  ).toBeNull();
  expect(
    screen.queryByRole("link", {
      name: "Открыть непроведённый отчёт МойСклад",
    }),
  ).toBeNull();
});

it("persists an unknown approval across remount and uses history reads without another write", async () => {
  const f = mountImport(undefined, true);
  await previewImport();
  await approveImport();
  await screen.findByRole("alert");
  expect(screen.queryByText(/PRIVATE_LOST_REPLY/)).toBeNull();
  f.unmount();
  provider(
    <OrdersCommissionImport
      scope={scope}
      config={{ ...config, finance: policy }}
    />,
  );
  await screen.findByText(
    /Результат подтверждения за 2026-10 пока не установлен/,
  );
  await screen.findByText("Финансовых операций пока нет.");
  expect(
    (
      screen.getByRole("button", {
        name: "Проверить черновик комиссионера",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  f.setOperations([work("UNKNOWN")]);
  fireEvent.click(
    screen.getByRole("button", { name: "Обновить историю отчётов" }),
  );
  await screen.findByText("Результат неизвестен — только проверка");
  expect(
    f.requests.filter((item) => item.path.endsWith("approve")),
  ).toHaveLength(1);
});

it("blocks source corrections and unsupported returns instead of silently rewriting or importing partial rows", async () => {
  const f = mountImport({
    plan: plan(),
    validation: [],
    existing_work: {
      ...work("RECONCILED"),
      document_id: id(9),
      receipt: { state: "UNKNOWN" },
      source_changed: true,
    },
    source_changed: true,
    source_diff: {
      before_fingerprint: "a".repeat(64),
      after_fingerprint: "b".repeat(64),
      header_changed: true,
      added_rows: [30],
      removed_rows: [31],
      changed_rows: Array.from({ length: 27 }, (_, index) => index + 1),
      component_changes: Array.from({ length: 27 }, (_, index) => ({
        source_row: index + 1,
        side: "SALE",
        component: "bank_coinvestment",
        kind: "CHANGED",
        before:
          index === 0 ? { presence: "missing" as const } : known(String(index)),
        after: known(index === 0 ? "9007199254740993.01" : String(index + 1)),
      })),
    },
  });
  await previewImport();
  expect(
    screen.getByText(/Требуется ручной разбор корректировки/),
  ).toBeTruthy();
  expect(screen.getByText("Заголовок отчёта изменён.")).toBeTruthy();
  expect(screen.getByText(/Изменены строки: 1, 2, 3.*25; ещё 2/)).toBeTruthy();
  expect(screen.getByTitle("a".repeat(64)).textContent).toBe("aaaaaaaaaaaa…");
  expect(
    screen.queryByRole("button", { name: "Создать непроведённый черновик" }),
  ).toBeNull();
  expect(
    screen
      .getByRole("link", { name: "Открыть непроведённый отчёт МойСклад" })
      .getAttribute("href"),
  ).toContain("commissionreportin/edit?id=");
  fireEvent.click(screen.getByText("Изменения финансовых компонентов (27)"));
  expect(
    screen.getByText("Изменён: Поле отсутствует → 9007199254740993.01."),
  ).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Следующие изменения" }));
  expect(screen.getByText("Изменён: 26 → 27.")).toBeTruthy();
  expect(f.requests).toHaveLength(1);
  f.unmount();
  mountImport({ validation: [{ code: "commission_returns_require_review" }] });
  await previewImport();
  expect(screen.getByText(/Финансовые возвраты не подтверждены/)).toBeTruthy();
  expect(
    screen.queryByRole("button", { name: "Создать непроведённый черновик" }),
  ).toBeNull();
});

it("requires a new explicit confirmation for a known unsent retry and preserves original work identity", async () => {
  const f = mountImport({
    id: "plan-1",
    plan: plan(),
    validation: [],
    existing_work: { ...work("BLOCKED", 0), id: "previous-work" },
  });
  await previewImport();
  fireEvent.click(screen.getByLabelText(/Я проверил полный период, договор/));
  expect(
    (
      screen.getByRole("button", {
        name: "Создать новую попытку черновика",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  fireEvent.click(
    screen.getByLabelText(/Подтверждаю новую попытку после доказанного отказа/),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Создать новую попытку черновика" }),
  );
  await waitFor(() => expect(f.requests).toHaveLength(2));
  expect(f.requests[1].body.retry_work_id).toBe("previous-work");
});

it.each([
  ["BLOCKED", "FINANCE_EXISTING_DOCUMENT_REQUIRES_REVIEW"],
  ["REJECTED", "FINANCE_EXISTING_DOCUMENT_REQUIRES_REVIEW"],
  ["BLOCKED", "FINANCE_EXISTING_DOCUMENT_UNVERIFIED"],
  ["REJECTED", "FINANCE_EXISTING_DOCUMENT_UNVERIFIED"],
])("keeps %s/%s read-only despite zero own dispatches", async (state, code) => {
  const f = mountImport({
    plan: plan(),
    validation: [],
    existing_work: { ...work(state, 0), code, id: "unresolved-document" },
  });
  await previewImport();
  expect(
    screen.getByText(/создание нового черновика за этот период заблокировано/),
  ).toBeTruthy();
  expect(
    screen.queryByRole("button", { name: "Создать новую попытку черновика" }),
  ).toBeNull();
  expect(
    screen.queryByRole("button", { name: "Создать непроведённый черновик" }),
  ).toBeNull();
  expect(
    screen.queryByLabelText(
      /Подтверждаю новую попытку после доказанного отказа/,
    ),
  ).toBeNull();
  fireEvent.click(
    screen.getByRole("button", { name: "Обновить историю отчётов" }),
  );
  await waitFor(() => expect(f.requests).toHaveLength(1));
  expect(f.requests[0].path).toContain("/finance/preview");
});

it("keeps saved financial history readable after policy revocation without starting a new import", async () => {
  const reads = vi.fn(async () => json({ operations: [work("RECONCILED")] }));
  vi.stubGlobal("fetch", reads);
  provider(<OrdersCommissionImport scope={scope} config={config} />);
  expect(reads).not.toHaveBeenCalled();
  expect(
    (
      screen.getByRole("button", {
        name: "Проверить черновик комиссионера",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(false);
  fireEvent.click(
    screen.getByRole("button", { name: "Обновить историю отчётов" }),
  );
  await screen.findByText("Непроведённый черновик создан и сверён");
  expect(reads).toHaveBeenCalledTimes(1);
});

it("keeps confirmed partial drafts visibly under accounting review", async () => {
  const value = work("RECONCILED");
  value.plan.accounting = {
    version: "realization-ledger-v1",
    source_fingerprint: value.plan.source.fingerprint,
    period: value.plan.source.period,
    currency: "RUB",
    rows_total: 1,
    rows_classified: 1,
    rows_mapped_to_native: 0,
    rows_partial: 1,
    rows_unmapped: 0,
    components_total: 1,
    components_represented: 0,
    components_unrepresented: 1,
    unknown_components: 0,
    complete: false,
    rows: [],
    components: [],
    category_totals: [],
    reconciliation: [],
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => json({ operations: [value] })),
  );
  provider(
    <OrdersCommissionImport
      scope={scope}
      config={{ ...config, finance: policy }}
    />,
  );
  await screen.findByText(
    "Непроведённый черновик сверён — остаток требует разбора",
  );
  expect(
    screen.getByText(
      /Финансовая сверка не завершена: остаток требует бухгалтера/,
    ),
  ).toBeTruthy();
  expect(
    screen.getByRole("link", { name: "Открыть непроведённый отчёт МойСклад" }),
  ).toBeTruthy();
});

it("shows a proven externally posted document as read-only without calling it an unposted draft", async () => {
  const value = work("UNKNOWN");
  value.document_id = id(9);
  value.code = "FINANCE_DOCUMENT_POSTED_EXTERNALLY";
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => json({ operations: [value] })),
  );
  provider(
    <OrdersCommissionImport
      scope={scope}
      config={{ ...config, finance: policy }}
    />,
  );
  await screen.findByText("Документ проведён вне STYK — нужна сверка");
  expect(
    screen.getByRole("link", { name: "Открыть проведённый отчёт МойСклад" }),
  ).toBeTruthy();
  expect(
    screen.queryByRole("link", {
      name: "Открыть непроведённый отчёт МойСклад",
    }),
  ).toBeNull();
  expect(
    screen.queryByRole("button", { name: "Создать новую попытку черновика" }),
  ).toBeNull();
});
