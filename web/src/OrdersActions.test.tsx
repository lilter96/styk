import { afterEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  OrdersActions,
  orderAllocationValid,
  ozonOrderActions,
} from "./OrdersActions";
import { Orders } from "./Orders";
import { OrdersPreview } from "./OrdersPreview";
import { OrdersOperations } from "./OrdersOperations";
import { safeOrderArtifactURL } from "./OrdersActionSummary";
import {
  orderScopeBody,
  type OrderActionOptions,
  type OrderPosting,
  type OrdersPreviewData,
  type OrdersConfigData,
  type OrderCommand,
} from "./OrdersTypes";

const scope = {
  tenant: "action-synthetic",
  moy: "synthetic-moy",
  ozon: "synthetic-ozon",
};
const missing = { presence: "missing" } as const;
const posting: OrderPosting = {
  scope: orderScopeBody(scope),
  commercial_order_id: "order-synthetic",
  posting_number: "POSTING-SYNTHETIC",
  parent_posting_number: missing,
  child_posting_numbers: [],
  order_number: missing,
  status: { presence: "value", value: "awaiting_packaging" },
  substatus: missing,
  scheme: { presence: "value", value: "rFBS" },
  warehouse_id: { presence: "value", value: "warehouse-synthetic" },
  lines: [
    {
      identity: { sku: "123", offer_id: "OFFER-SYNTHETIC" },
      quantity: "2",
      name: { presence: "value", value: "Тестовый товар" },
      price: missing,
    },
  ],
  financial: { order_total: missing },
  times: { created_at: missing },
  evidence: {
    revision: "source-v1",
    checked_at: new Date().toISOString(),
    observed_at: new Date().toISOString(),
    provider_updated_at: missing,
  },
  drift: [],
};
const options = (
  extra: Partial<OrderActionOptions> = {},
): OrderActionOptions => ({
  scope: orderScopeBody(scope),
  posting_number: posting.posting_number,
  source_revision: "source-v1",
  actions: ozonOrderActions.map((action) => ({
    action,
    allowed: true,
    blockers: [],
    parameters_required: [],
  })),
  cancellation_reasons: [{ id: "11", title: "Тестовая причина Ozon" }],
  label_types: ["big_label", "small_label"],
  ...extra,
});
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
  store_id: "store",
  state_id: "state",
  sales_channel_id: "channel",
  field_mappings: [],
  status_mappings: [],
};
const json = (value: unknown) =>
  new Response(JSON.stringify(value), {
    headers: { "Content-Type": "application/json" },
  });
function mockAPI(
  custom?: (url: URL, init?: RequestInit) => Response | undefined,
) {
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    const url = new URL(path, "http://localhost");
    const response = custom?.(url, init);
    if (response) return response;
    if (url.pathname.endsWith("/action-options")) return json(options());
    if (url.pathname.endsWith("/config")) return json(config);
    if (url.pathname.endsWith("/metadata/registry"))
      return json({
        scope: orderScopeBody(scope),
        revision: 0,
        attributes: [],
      });
    if (url.pathname.endsWith("/metadata"))
      return json({
        scope: orderScopeBody(scope),
        organizations: [],
        agents: [],
        stores: [],
        states: [],
        sales_channels: [],
        currencies: [],
        attributes: [],
      });
    if (url.pathname.endsWith("/postings"))
      return json({ postings: [posting] });
    if (url.pathname.endsWith("/operations")) return json({ operations: [] });
    if (url.pathname.endsWith("/approve"))
      return json({
        id: "operation-synthetic",
        scope: orderScopeBody(scope),
        state: "QUEUED",
        targets: [],
      });
    throw new Error(`Unexpected synthetic action endpoint ${url.pathname}`);
  });
  vi.stubGlobal("fetch", fetcher);
  return fetcher;
}
function mount(child: React.ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  return render(
    <QueryClientProvider client={client}>{child}</QueryClientProvider>,
  );
}
async function openAction(action?: string) {
  const details = screen
    .getByText("Действия с отправлением в Ozon")
    .closest("details")!;
  details.open = true;
  fireEvent(details, new Event("toggle"));
  const select = await screen.findByLabelText(
    `Действие для отправления ${posting.posting_number}`,
  );
  if (action) fireEvent.change(select, { target: { value: action } });
  return select;
}
const checkButton = () =>
  screen.getByRole("button", {
    name: "Проверить действие в Ozon",
  }) as HTMLButtonElement;
function form() {
  const onPreview = vi.fn();
  mount(
    <OrdersActions
      scope={scope}
      posting={posting}
      disabled={false}
      onPreview={onPreview}
      onDraftChange={() => {}}
    />,
  );
  return onPreview;
}
const command = (action = "posting_tracking"): OrderCommand => ({
  id: "command-synthetic",
  action,
  posting_number: posting.posting_number,
  correlation: {},
  posting_action: { parameters: { tracking_provided: true } },
});
const preview = (cmd = command()): OrdersPreviewData => ({
  id: "preview-synthetic",
  scope: orderScopeBody(scope),
  config_revision: 1,
  expires_at: new Date(Date.now() + 60000).toISOString(),
  decisions: [
    { posting, ready: true, validation: [], correlation: {}, commands: [cmd] },
  ],
});
afterEach(() => {
  cleanup();
  sessionStorage.clear();
  vi.unstubAllGlobals();
});

it("reads action options only when opened and never posts while inputs change", async () => {
  const fetcher = mockAPI();
  form();
  expect(fetcher).not.toHaveBeenCalled();
  await openAction("posting_tracking");
  fireEvent.change(screen.getByLabelText("Трек-номер"), {
    target: { value: "TRACK-SYNTHETIC" },
  });
  expect(fetcher.mock.calls).toHaveLength(1);
  expect(fetcher.mock.calls[0][1]?.method).toBeUndefined();
  expect(sessionStorage.length).toBe(0);
});
it("requires exact package allocation and explicit actual readiness without filling claims", async () => {
  mockAPI();
  const selected = form();
  await openAction("posting_pack");
  expect(checkButton().disabled).toBe(true);
  const quantity = screen.getByLabelText(
    "Количество OFFER-SYNTHETIC в упаковке 1",
  );
  expect((quantity as HTMLInputElement).value).toBe("");
  fireEvent.change(quantity, { target: { value: "1" } });
  expect(checkButton().disabled).toBe(true);
  fireEvent.change(quantity, { target: { value: "2" } });
  expect(checkButton().disabled).toBe(true);
  fireEvent.click(
    screen.getByLabelText(
      /Подтверждаю, что указанные товары фактически собраны/,
    ),
  );
  fireEvent.click(checkButton());
  expect(selected).toHaveBeenCalledWith({
    posting_number: posting.posting_number,
    action: "posting_pack",
    parameters: {
      readiness_confirmed: true,
      packages: [
        { lines: [{ identity: posting.lines[0].identity, quantity: "2" }] },
      ],
    },
  });
  fireEvent.change(quantity, { target: { value: "1" } });
  expect(
    (
      screen.getByLabelText(
        /Подтверждаю, что указанные товары фактически собраны/,
      ) as HTMLInputElement
    ).checked,
  ).toBe(false);
});
it("requires two nonempty packages for a split and rejects over-allocation exactly", async () => {
  mockAPI();
  const selected = form();
  await openAction("posting_split");
  fireEvent.change(
    screen.getByLabelText("Количество OFFER-SYNTHETIC в упаковке 1"),
    { target: { value: "1" } },
  );
  fireEvent.click(screen.getByRole("button", { name: "Добавить упаковку" }));
  fireEvent.change(
    screen.getByLabelText("Количество OFFER-SYNTHETIC в упаковке 2"),
    { target: { value: "1" } },
  );
  fireEvent.click(
    screen.getByLabelText(
      /Подтверждаю, что указанные товары фактически собраны/,
    ),
  );
  fireEvent.click(checkButton());
  expect(selected.mock.calls[0][0].parameters.packages).toHaveLength(2);
  const key = JSON.stringify(posting.lines[0].identity);
  expect(
    orderAllocationValid(posting, [{ [key]: "9007199254740993.01" }], false),
  ).toBe(false);
  expect(
    orderAllocationValid(posting, [{ [key]: "1.01" }, { [key]: "0.99" }], true),
  ).toBe(true);
});
it("uses provider cancellation reason IDs and validates partial quantities", async () => {
  mockAPI();
  const selected = form();
  await openAction("posting_partial_cancel");
  fireEvent.change(screen.getByLabelText("Причина отмены"), {
    target: { value: "11" },
  });
  fireEvent.change(
    screen.getByLabelText("Количество OFFER-SYNTHETIC в отмене"),
    { target: { value: "3" } },
  );
  expect(checkButton().disabled).toBe(true);
  fireEvent.change(
    screen.getByLabelText("Количество OFFER-SYNTHETIC в отмене"),
    { target: { value: "1" } },
  );
  fireEvent.click(checkButton());
  expect(selected.mock.calls[0][0].parameters).toEqual({
    reason_id: "11",
    lines: [{ identity: posting.lines[0].identity, quantity: "1" }],
  });
});
it("requires an operator-entered event reference and actual-event confirmation for delivery", async () => {
  mockAPI();
  const selected = form();
  await openAction("posting_deliver");
  const reference = screen.getByLabelText(
    "Основание фактического события",
  ) as HTMLInputElement;
  expect(reference.value).toBe("");
  expect(checkButton().disabled).toBe(true);
  fireEvent.change(reference, { target: { value: "ACT-SYNTHETIC-1" } });
  expect(checkButton().disabled).toBe(true);
  fireEvent.click(
    screen.getByLabelText(
      "Подтверждаю, что покупатель фактически получил отправление.",
    ),
  );
  fireEvent.click(checkButton());
  expect(selected.mock.calls[0][0].parameters).toEqual({
    event_reference: "ACT-SYNTHETIC-1",
    event_confirmed: true,
  });
  fireEvent.change(reference, { target: { value: "ACT-SYNTHETIC-2" } });
  expect(checkButton().disabled).toBe(true);
});
it("shows action-specific blockers and keeps blocked actions unconfirmable", async () => {
  mockAPI((url) =>
    url.pathname.endsWith("/action-options")
      ? json(
          options({
            actions: [
              {
                action: "posting_ship",
                allowed: false,
                blockers: ["status_not_allowed"],
                parameters_required: [],
              },
            ],
          }),
        )
      : undefined,
  );
  form();
  await openAction("posting_ship");
  expect(
    screen.getByText(/Текущий статус отправления не допускает/),
  ).toBeTruthy();
  expect(checkButton().disabled).toBe(true);
});
it("does not offer a synthetic FBS handover even when an option is incorrectly marked allowed", async () => {
  mockAPI();
  const onPreview = vi.fn();
  mount(
    <OrdersActions
      scope={scope}
      posting={{ ...posting, scheme: { presence: "value", value: "FBS" } }}
      disabled={false}
      onPreview={onPreview}
      onDraftChange={() => {}}
    />,
  );
  await openAction("posting_ship");
  fireEvent.change(screen.getByLabelText("Основание фактического события"), {
    target: { value: "ACT-SYNTHETIC" },
  });
  fireEvent.click(
    screen.getByLabelText(
      "Подтверждаю, что отправление rFBS фактически передано в доставку.",
    ),
  );
  expect(
    screen.getByText(
      /Передача статуса «В доставке» здесь доступна только для rFBS/,
    ),
  ).toBeTruthy();
  expect(checkButton().disabled).toBe(true);
  expect(onPreview).not.toHaveBeenCalled();
});
it("rejects action options from another account before an intent can be selected", async () => {
  mockAPI((url) =>
    url.pathname.endsWith("/action-options")
      ? json(
          options({
            scope: {
              ...orderScopeBody(scope),
              ozon_account: "another-account",
            },
          }),
        )
      : undefined,
  );
  form();
  const details = screen
    .getByText("Действия с отправлением в Ozon")
    .closest("details")!;
  details.open = true;
  fireEvent(details, new Event("toggle"));
  await screen.findByRole("alert");
  expect(
    screen.queryByLabelText(
      `Действие для отправления ${posting.posting_number}`,
    ),
  ).toBeNull();
});
it("reviews and separately approves a scoped tracking proposal while the returned log remains redacted", async () => {
  const fetcher = mockAPI((url) =>
    url.pathname.endsWith("/preview") ? json(preview()) : undefined,
  );
  mount(<Orders scope={scope} />);
  await screen.findByLabelText(`Выбрать отправление ${posting.posting_number}`);
  await openAction("posting_tracking");
  fireEvent.change(screen.getByLabelText("Трек-номер"), {
    target: { value: "TRACK-SYNTHETIC" },
  });
  fireEvent.click(checkButton());
  await screen.findByRole("heading", { name: "Проверка действия в Ozon" });
  expect(screen.getByText("Трек-номер введён; скрыт в журнале.")).toBeTruthy();
  const request = fetcher.mock.calls.find(([path]) =>
    path.endsWith("/preview"),
  )!;
  expect(JSON.parse(request[1]!.body as string)).toEqual({
    ...orderScopeBody(scope),
    expected_revision: 1,
    posting_numbers: [posting.posting_number],
    actions: ["posting_tracking"],
    proposals: [
      {
        posting_number: posting.posting_number,
        action: "posting_tracking",
        parameters: { tracking_number: "TRACK-SYNTHETIC" },
      },
    ],
  });
  expect(fetcher.mock.calls.some(([path]) => path.endsWith("/approve"))).toBe(
    false,
  );
  fireEvent.click(
    screen.getByLabelText(`Подтвердить отправление ${posting.posting_number}`),
  );
  expect(
    (
      screen.getByRole("button", {
        name: "Подтвердить действие в Ozon (1)",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  fireEvent.click(
    screen.getByLabelText(/Я проверил параметры выбранного действия/),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Подтвердить действие в Ozon (1)" }),
  );
  await screen.findByText(/Подтверждение записано/);
  expect(
    fetcher.mock.calls.filter(([path]) => path.endsWith("/approve")),
  ).toHaveLength(1);
  expect(sessionStorage.length).toBe(0);
});
it("never accepts a different action returned by a preview", () => {
  mount(
    <OrdersPreview
      preview={preview(command("posting_cancel"))}
      permittedActions={["posting_tracking"]}
      blocked={false}
      busy={false}
      onApprove={() => {}}
    />,
  );
  expect(
    (
      screen.getByLabelText(
        `Подтвердить отправление ${posting.posting_number}`,
      ) as HTMLInputElement
    ).disabled,
  ).toBe(true);
});
it("exposes label downloads only after reconciliation and rejects external artifact URLs", async () => {
  const artifact = "/api/orders/artifacts/work-synthetic?type=big_label";
  expect(
    safeOrderArtifactURL("https://example.invalid/label.pdf"),
  ).toBeUndefined();
  expect(
    safeOrderArtifactURL("/api/orders/artifacts/../../session"),
  ).toBeUndefined();
  mockAPI((url) =>
    url.pathname.endsWith("/operations")
      ? json({
          operations: [
            {
              id: "operation-synthetic",
              scope: orderScopeBody(scope),
              created_at: new Date().toISOString(),
              state: "RECONCILED",
              targets: ["ACKNOWLEDGED", "RECONCILED"].map((state, index) => ({
                id: `work-${index}`,
                state,
                command: command("posting_label_create"),
                automatic: false,
                receipt: {
                  state,
                  artifact: { available: true, download_url: artifact },
                  task_ids: [{ id: `task-${index}`, type: "label" }],
                },
              })),
            },
          ],
        })
      : undefined,
  );
  mount(<OrdersOperations scope={scope} />);
  const download = await screen.findAllByRole("link", {
    name: "Скачать этикетку",
  });
  expect(download).toHaveLength(1);
  expect(download[0].getAttribute("href")).toBe(artifact);
  expect(screen.queryByRole("link", { name: /Заказ МойСклад/ })).toBeNull();
});
