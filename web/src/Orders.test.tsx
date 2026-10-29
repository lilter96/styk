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
import { Orders } from "./Orders";
import { OrdersPreview } from "./OrdersPreview";
import { OrdersPostings, orderQuantity } from "./OrdersPostings";
import {
  orderFieldText,
  orderScopeBody,
  type OrderScope,
  type OrderPosting,
  type OrdersConfigData,
  type OrdersPreviewData,
  type OrderDecision,
  type OrdersMetadata,
  type OrderField,
} from "./OrdersTypes";

const scope = {
  tenant: "synthetic",
  moy: "moy-synthetic",
  ozon: "ozon-synthetic",
};
const value = <T,>(value: T): OrderField<T> => ({ presence: "value", value });
const missing = { presence: "missing" } as const;
const ref = (id: string, name: string) => ({ id, name });
const metadata: OrdersMetadata = {
  scope: orderScopeBody(scope),
  organizations: [ref("org", "Тестовая организация")],
  agents: [ref("agent", "Тестовый контрагент")],
  stores: [ref("store", "Тестовый склад")],
  states: [ref("state", "Новый заказ")],
  sales_channels: [ref("channel", "Ozon")],
  currencies: [{ id: "currency", iso_code: "RUB", default: true }],
  attributes: [],
};
const configuration = (
  extra: Partial<OrdersConfigData> = {},
): OrdersConfigData => ({
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
  store_id: "store",
  state_id: "state",
  sales_channel_id: "channel",
  currency_id: "currency",
  field_mappings: [{ source: "posting_number", target: "name" }],
  status_mappings: [],
  ...extra,
});
const posting = (
  number = "SYNTHETIC-1",
  currentScope = scope,
): OrderPosting => ({
  scope: orderScopeBody(currentScope),
  commercial_order_id: "order-synthetic",
  posting_number: number,
  parent_posting_number: missing,
  child_posting_numbers: [],
  order_number: value("ORDER-SYNTHETIC"),
  status: value("awaiting_packaging"),
  substatus: missing,
  scheme: value("FBS"),
  warehouse_id: value("warehouse-synthetic"),
  lines: [
    {
      identity: { offer_id: "SYNTHETIC-SKU" },
      quantity: "0",
      name: value("Тестовый товар"),
      price: { presence: "zero", value: { amount: "0", currency: "RUB" } },
    },
  ],
  financial: { order_total: missing },
  times: { created_at: missing },
  evidence: {
    revision: "v1",
    observed_at: "2026-10-29T12:00:00Z",
    checked_at: "2026-10-29T12:01:00Z",
    provider_updated_at: missing,
  },
  drift: [],
});
const decision = (number = "SYNTHETIC-1", ready = true): OrderDecision => ({
  posting: posting(number),
  ready,
  correlation: {},
  validation: ready
    ? []
    : [{ code: "source_stale", field: "evidence.checked_at" }],
  commands: ready
    ? [
        {
          id: `command-${number}`,
          action: "mirror_create",
          posting_number: number,
          correlation: {},
          customer_order: {
            organization_id: "org",
            agent_id: "agent",
            store_id: "store",
            state_id: "state",
            sales_channel_id: "channel",
            name: value(number),
            description: missing,
            moment: missing,
            applicable: value(false),
            positions: [],
          },
        },
      ]
    : [],
});
const preview = (decisions = [decision()]): OrdersPreviewData => ({
  id: "preview-synthetic",
  scope: orderScopeBody(scope),
  config_revision: 1,
  expires_at: new Date(Date.now() + 60000).toISOString(),
  decisions,
});
const json = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  });
function mockAPI(
  handler?: (url: URL, init?: RequestInit) => Response | undefined,
) {
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    const url = new URL(path, "http://localhost");
    const custom = handler?.(url, init);
    if (custom) return custom;
    if (url.pathname.endsWith("/config")) return json(configuration());
    if (url.pathname.endsWith("/metadata")) return json(metadata);
    if (url.pathname.endsWith("/postings"))
      return json({ postings: [posting()] });
    if (url.pathname.endsWith("/operations")) return json({ operations: [] });
    if (url.pathname.endsWith("/preview")) return json(preview());
    if (url.pathname.endsWith("/approve"))
      return json({
        id: "operation-synthetic",
        scope: orderScopeBody(scope),
        targets: [],
        state: "QUEUED",
      });
    throw new Error(`Unexpected synthetic endpoint ${url.pathname}`);
  });
  vi.stubGlobal("fetch", fetcher);
  return fetcher;
}
function mount(component = <Orders accountScope={scope} />) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  const wrap = (child: typeof component) => (
    <QueryClientProvider client={client}>{child}</QueryClientProvider>
  );
  const rendered = render(wrap(component));
  return {
    ...rendered,
    changeScope: (next: OrderScope) =>
      rendered.rerender(wrap(<Orders accountScope={next} />)),
  };
}
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

it("preserves missing, null, empty, false and exact zero instead of inventing values", () => {
  expect(orderFieldText(missing)).toBe("Неизвестно");
  expect(orderFieldText({ presence: "null" })).toBe("Источник передал null");
  expect(orderFieldText({ presence: "empty" })).toBe("Пустое значение");
  expect(orderFieldText({ presence: "zero", value: "0" })).toBe("0");
  expect(orderFieldText(value(false))).toBe("Нет");
  const line = posting().lines[0];
  expect(orderQuantity([line])).toBe("0");
  expect(
    orderQuantity([
      { ...line, quantity: "9007199254740993.01" },
      { ...line, quantity: "0.09" },
    ]),
  ).toBe("9007199254740993.1");
  expect(orderQuantity([])).toBe("Неизвестно");
  expect(orderQuantity([{ ...line, quantity: "" }])).toBe("Неизвестно");
});

it("shows unknown source total and provider update separately from known zero quantity and check time", async () => {
  mockAPI();
  mount();
  const choice = await screen.findByLabelText(
    "Выбрать отправление SYNTHETIC-1",
  );
  const article = choice.closest("article")!;
  expect(within(article).getByText("0", { exact: true })).toBeTruthy();
  expect(
    within(article).getByText("Сумма Ozon").nextElementSibling?.textContent,
  ).toBe("Неизвестно");
  expect(
    within(article).getByText("Обновлено в источнике").nextElementSibling
      ?.textContent,
  ).toBe("Неизвестно");
  expect(
    within(article).getByText("Проверено STYK").nextElementSibling?.textContent,
  ).toContain("29.10.2026");
});

it("requires separate explicit approval, selects only ready decisions, and never writes on view", async () => {
  const fetcher = mockAPI((url) =>
    url.pathname.endsWith("/postings")
      ? json({ postings: [posting(), posting("SYNTHETIC-2")] })
      : url.pathname.endsWith("/preview")
        ? json(preview([decision(), decision("SYNTHETIC-2", false)]))
        : undefined,
  );
  mount();
  fireEvent.click(
    await screen.findByLabelText("Выбрать отправление SYNTHETIC-1"),
  );
  fireEvent.click(screen.getByLabelText("Выбрать отправление SYNTHETIC-2"));
  expect(
    fetcher.mock.calls.filter(([, init]) => init?.method === "POST"),
  ).toHaveLength(0);
  fireEvent.click(
    screen.getByRole("button", { name: "Проверить выбранные отправления" }),
  );
  await screen.findByRole("heading", { name: "Проверка перед передачей" });
  expect(
    (
      screen.getByLabelText(
        "Подтвердить отправление SYNTHETIC-2",
      ) as HTMLInputElement
    ).disabled,
  ).toBe(true);
  fireEvent.click(
    screen.getByRole("button", { name: "Выбрать все готовые отправления" }),
  );
  expect(
    (
      screen.getByRole("button", {
        name: "Передать в МойСклад (1)",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  fireEvent.click(screen.getByLabelText(/Я проверил состав и поля/));
  fireEvent.click(
    screen.getByRole("button", { name: "Передать в МойСклад (1)" }),
  );
  await screen.findByText(/Подтверждение записано/);
  const approvals = fetcher.mock.calls.filter(([path]) =>
    path.includes("/approve"),
  );
  expect(approvals).toHaveLength(1);
  expect(JSON.parse(approvals[0][1]!.body as string)).toEqual({
    ...orderScopeBody(scope),
    preview_id: "preview-synthetic",
    posting_numbers: ["SYNTHETIC-1"],
    acknowledge: true,
    idempotency_key: expect.any(String),
  });
});

it("clears selected orders, preview and unsaved settings when either account changes", async () => {
  const next = { ...scope, ozon: "ozon-other-synthetic" };
  mockAPI((url) => {
    if (url.searchParams.get("ozon_account") !== next.ozon) return;
    if (url.pathname.endsWith("/config"))
      return json(configuration({ scope: orderScopeBody(next) }));
    if (url.pathname.endsWith("/metadata"))
      return json({ ...metadata, scope: orderScopeBody(next) });
    if (url.pathname.endsWith("/postings"))
      return json({ postings: [posting("OTHER-1", next)] });
  });
  const mounted = mount();
  fireEvent.click(
    await screen.findByLabelText("Выбрать отправление SYNTHETIC-1"),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Проверить выбранные отправления" }),
  );
  await screen.findByRole("heading", { name: "Проверка перед передачей" });
  fireEvent.change(screen.getByLabelText("Интервал проверки, секунд"), {
    target: { value: "120" },
  });
  mounted.changeScope(next);
  await screen.findByLabelText("Выбрать отправление OTHER-1");
  expect(screen.queryByLabelText("Выбрать отправление SYNTHETIC-1")).toBeNull();
  expect(
    screen.queryByRole("heading", { name: "Проверка перед передачей" }),
  ).toBeNull();
  expect(screen.getByText("Выбрано отправлений: 0")).toBeTruthy();
  expect(
    (screen.getByLabelText("Интервал проверки, секунд") as HTMLInputElement)
      .value,
  ).toBe("300");
});

it("does not retry or reapprove an unknown outcome and exposes read-only history refresh", async () => {
  const fetcher = mockAPI((url) =>
    url.pathname.endsWith("/approve")
      ? json({ error: "UNAVAILABLE" }, 503)
      : undefined,
  );
  mount();
  fireEvent.click(
    await screen.findByLabelText("Выбрать отправление SYNTHETIC-1"),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Проверить выбранные отправления" }),
  );
  fireEvent.click(
    await screen.findByLabelText("Подтвердить отправление SYNTHETIC-1"),
  );
  fireEvent.click(screen.getByLabelText(/Я проверил состав и поля/));
  fireEvent.click(
    screen.getByRole("button", { name: "Передать в МойСклад (1)" }),
  );
  await screen.findByText(/Результат подтверждения неизвестен:/);
  expect(
    (
      screen.getByRole("button", {
        name: "Передать в МойСклад (1)",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  fireEvent.click(
    screen.getByRole("button", { name: "Обновить статусы заказов" }),
  );
  await waitFor(() =>
    expect(
      fetcher.mock.calls.filter(([path]) => path.includes("/approve")),
    ).toHaveLength(1),
  );
});

it.each([true, false])(
  "uses actual native references and enables test eligibility only when available=%s",
  async (available) => {
    const fetcher = mockAPI((url, init) =>
      url.pathname.endsWith("/config")
        ? json(
            init?.method === "POST"
              ? { ...JSON.parse(init.body as string).config, revision: 2 }
              : configuration({
                  enabled: false,
                  test_enabled: false,
                  publication_available: available,
                }),
          )
        : undefined,
    );
    mount();
    await screen.findByRole("option", { name: "Тестовая организация" });
    fireEvent.click(
      screen.getByLabelText("Разрешить передачу заказов в МойСклад"),
    );
    expect(
      (
        screen.getByRole("button", {
          name: "Сохранить настройки заказов",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
    fireEvent.click(
      screen.getByLabelText(/Разрешаю создание и обновление заказов/),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Сохранить настройки заказов" }),
    );
    await screen.findByText("Настройки заказов сохранены.");
    const body = JSON.parse(
      fetcher.mock.calls.find(([, init]) => init?.method === "POST")![1]!
        .body as string,
    );
    expect(body.config.organization_id).toBe("org");
    expect(body.config.state_id).toBe("state");
    expect(body.config.enabled).toBe(true);
    expect(body.config.test_enabled).toBe(available);
    expect(body.config.publication_available).toBeUndefined();
  },
);

it("debounces server search and requests only the next bounded page", async () => {
  const fetcher = mockAPI((url) =>
    url.pathname.endsWith("/postings")
      ? json({
          postings: [posting()],
          next_cursor: url.searchParams.get("cursor")
            ? undefined
            : "next-synthetic",
        })
      : undefined,
  );
  mount(
    <OrdersPostings
      scope={scope}
      selected={[]}
      disabled={false}
      uncertain={[]}
      links={{}}
      onSelected={() => {}}
    />,
  );
  const nextPage = await screen.findByRole("button", {
    name: "Следующие отправления",
  });
  await waitFor(() =>
    expect((nextPage as HTMLButtonElement).disabled).toBe(false),
  );
  fireEvent.click(nextPage);
  await waitFor(() =>
    expect(
      fetcher.mock.calls.some(
        ([path]) =>
          new URL(path, "http://localhost").searchParams.get("cursor") ===
          "next-synthetic",
      ),
    ).toBe(true),
  );
  fireEvent.change(screen.getByLabelText("Найти отправление или заказ"), {
    target: { value: "SYNTHETIC-SEARCH" },
  });
  expect(
    fetcher.mock.calls.some(([path]) => path.includes("SYNTHETIC-SEARCH")),
  ).toBe(false);
  await waitFor(() =>
    expect(
      fetcher.mock.calls.some(([path]) => {
        const url = new URL(path, "http://localhost");
        return (
          url.searchParams.get("search") === "SYNTHETIC-SEARCH" &&
          url.searchParams.get("cursor") === "" &&
          url.searchParams.get("limit") === "25"
        );
      }),
    ).toBe(true),
  );
});

it("keeps native controls focusable and expires a preview without sending an approval", () => {
  const approve = vi.fn();
  mount(
    <OrdersPreview
      preview={{
        ...preview(),
        expires_at: new Date(Date.now() - 1000).toISOString(),
      }}
      blocked={false}
      busy={false}
      onApprove={approve}
    />,
  );
  const choice = screen.getByLabelText(
    "Подтвердить отправление SYNTHETIC-1",
  ) as HTMLInputElement;
  expect(choice.disabled).toBe(true);
  expect(screen.getByText(/Срок проверки истёк/)).toBeTruthy();
  fireEvent.click(
    screen.getByRole("button", { name: "Передать в МойСклад (0)" }),
  );
  expect(approve).not.toHaveBeenCalled();
});

it("requires a bounded action and field grant plus explicit consent for automation", async () => {
  const fetcher = mockAPI((url, init) =>
    url.pathname.endsWith("/config") && init?.method === "POST"
      ? json({ ...JSON.parse(init.body as string).config, revision: 2 })
      : undefined,
  );
  mount();
  await screen.findByRole("option", { name: "Тестовая организация" });
  fireEvent.click(
    screen.getByLabelText("Автоматически передавать выбранные поля"),
  );
  const save = screen.getByRole("button", {
    name: "Сохранить настройки заказов",
  }) as HTMLButtonElement;
  expect(save.disabled).toBe(true);
  const fields = screen.getByRole("group", { name: "Разрешённые поля" });
  fireEvent.click(
    within(fields).getByLabelText("Номер отправления → название заказа"),
  );
  fireEvent.click(within(fields).getByLabelText("Состояние заказа МойСклад"));
  expect(save.disabled).toBe(true);
  fireEvent.click(screen.getByLabelText(/Разрешаю автоматическое выполнение/));
  fireEvent.click(save);
  await screen.findByText("Настройки заказов сохранены.");
  const body = JSON.parse(
    fetcher.mock.calls.find(([, init]) => init?.method === "POST")![1]!
      .body as string,
  );
  expect(body.config.automation.actions).toEqual([
    "mirror_create",
    "mirror_update",
  ]);
  expect(body.config.automation.fields).toEqual(["name", "state"]);
  expect(
    Date.parse(body.config.automation.expires_at) - Date.now(),
  ).toBeLessThanOrEqual(3600000);
  expect(
    Date.parse(body.config.automation.expires_at) - Date.now(),
  ).toBeGreaterThan(3500000);
  expect(
    fetcher.mock.calls.filter(([path]) => path.includes("/approve")),
  ).toHaveLength(0);
});

it("blocks a previously unknown posting from the paginated history and shows its native order link", async () => {
  mockAPI((url) =>
    url.pathname.endsWith("/operations")
      ? json({
          operations: [
            {
              id: "operation-unknown",
              scope: orderScopeBody(scope),
              state: "UNKNOWN",
              created_at: "2026-10-29T12:00:00Z",
              targets: [
                {
                  id: "work-unknown",
                  state: "UNKNOWN",
                  automatic: false,
                  command: decision().commands[0],
                  receipt: {
                    state: "UNKNOWN",
                    external_id: "moy-order-synthetic",
                  },
                },
              ],
            },
          ],
        })
      : undefined,
  );
  mount();
  await screen.findByText(/Запрос мог выполниться/);
  await waitFor(() =>
    expect(
      (
        screen.getByLabelText(
          "Выбрать отправление SYNTHETIC-1",
        ) as HTMLInputElement
      ).disabled,
    ).toBe(true),
  );
  const links = screen.getAllByRole("link", {
    name: "Заказ МойСклад moy-order-synthetic",
  });
  expect(links.length).toBeGreaterThan(0);
  expect(links[0].getAttribute("href")).toContain(
    "customerorder/edit?id=moy-order-synthetic",
  );
});
