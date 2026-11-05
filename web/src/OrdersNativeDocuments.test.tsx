import "@testing-library/jest-dom/vitest";
import { afterEach, expect, it, vi } from "vitest";
import {
  cleanup,
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  OrdersNativeDocuments,
  nativeQuantityAtMost,
  nativeQuantityTotalAtMost,
} from "./OrdersNativeDocuments";
import { orderScopeBody, type OrderPosting } from "./OrdersTypes";

const scope = { tenant: "native-ui", moy: "moy-account", ozon: "ozon-account" };
const missing = { presence: "missing" } as const;
const posting: OrderPosting = {
  scope: orderScopeBody(scope),
  commercial_order_id: "order",
  posting_number: "POSTING-1",
  parent_posting_number: missing,
  child_posting_numbers: [],
  order_number: missing,
  status: { presence: "value", value: "delivered" },
  substatus: missing,
  scheme: { presence: "value", value: "FBS" },
  warehouse_id: { presence: "value", value: "123" },
  lines: [
    {
      identity: {
        provider_line_id: "line-1",
        sku: "9007199254740993",
        offer_id: "TEST",
      },
      quantity: "3",
      name: { presence: "value", value: "Тестовый товар" },
      price: missing,
    },
  ],
  financial: { order_total: missing },
  times: { created_at: missing },
  evidence: {
    revision: "source-1",
    provider_updated_at: missing,
    observed_at: new Date().toISOString(),
  },
  drift: [],
};
function policy() {
  return {
    enabled: true,
    allow_posted: true,
    actions: ["demand_create", "salesreturn_create"],
    maximum_quantity: "10",
    event_max_age_seconds: 3600,
    attested_at: new Date(Date.now() - 60000).toISOString(),
    valid_until: new Date(Date.now() + 3600000).toISOString(),
  };
}
function response(value: unknown) {
  return new Response(JSON.stringify(value), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
}
function localTime() {
  const date = new Date(Date.now() - 5000);
  return new Date(date.getTime() - date.getTimezoneOffset() * 60000)
    .toISOString()
    .slice(0, -1);
}
function setup(options: { history?: unknown; nativePolicy?: unknown } = {}) {
  const fetch = vi.fn<(input: string, init?: RequestInit) => Promise<Response>>(
    async (input: string) => {
      const path = new URL(input, "http://localhost");
      if (path.pathname.endsWith("/config"))
        return response({
          scope: orderScopeBody(scope),
          enabled: true,
          test_enabled: true,
          publication_available: true,
          native_documents: options.nativePolicy ?? policy(),
        });
      if (path.pathname.endsWith("/operations"))
        return response(options.history ?? { operations: [] });
      throw new Error("unexpected request");
    },
  );
  vi.stubGlobal("fetch", fetch);
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const onPreview = vi.fn(),
    onDraftChange = vi.fn();
  const props = { scope, posting, disabled: false, onPreview, onDraftChange };
  const view = render(
    <QueryClientProvider client={client}>
      <OrdersNativeDocuments {...props} />
    </QueryClientProvider>,
  );
  return { fetch, client, onPreview, onDraftChange, view, props };
}
async function open() {
  const details = screen
    .getByText("Отгрузка и возврат в МойСклад")
    .closest("details");
  if (!details) throw new Error("missing form");
  details.open = true;
  fireEvent(details, new Event("toggle"));
  await screen.findAllByText(/лимит разрешения: 10/);
}
function quantity() {
  fireEvent.change(screen.getByLabelText(/Количество · Тестовый товар/), {
    target: { value: "1" },
  });
}
function fillEvent() {
  fireEvent.change(screen.getByLabelText("Фактическое время события"), {
    target: { value: localTime() },
  });
  fireEvent.change(screen.getByLabelText("Идентификатор реального события"), {
    target: { value: "synthetic-physical-event" },
  });
}
const previewButton = () =>
  screen.getByRole("button", { name: "Проверить документ перед созданием" });
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  vi.useRealTimers();
});

it("keeps exact quantities and never coerces provider identifiers to numbers", () => {
  expect(
    nativeQuantityAtMost(
      "9007199254740993.000000000000000001",
      "9007199254740993.000000000000000002",
    ),
  ).toBe(true);
  expect(
    nativeQuantityAtMost(
      "9007199254740993.000000000000000003",
      "9007199254740993.000000000000000002",
    ),
  ).toBe(false);
  for (const value of [
    "0",
    "-1",
    "1e2",
    "NaN",
    "Infinity",
    "0.0",
    "1".repeat(129),
  ])
    expect(nativeQuantityAtMost(value, "10")).toBe(false);
});

it("previews an explicit harmless draft without sending a mutation", async () => {
  const f = setup();
  expect(f.fetch).not.toHaveBeenCalled();
  await open();
  quantity();
  fireEvent.change(screen.getByLabelText("Плановое время документа"), {
    target: { value: "2026-12-01T12:34:56.123" },
  });
  fireEvent.change(screen.getByLabelText("Идентификатор планирования"), {
    target: { value: "synthetic-draft-plan" },
  });
  expect(previewButton()).not.toBeDisabled();
  fireEvent.click(previewButton());
  expect(f.onPreview).toHaveBeenCalledWith(
    expect.objectContaining({
      action: "demand_create",
      applicable: { presence: "zero", value: false },
      event_kind: "draft_plan",
      event_confirmed: false,
      event_reference: "synthetic-draft-plan",
      lines: [{ identity: posting.lines[0].identity, quantity: "1" }],
    }),
  );
  expect(f.onPreview.mock.calls[0][0]).not.toHaveProperty("event_occurred_at");
  expect(
    f.fetch.mock.calls.some(([path]) =>
      new URL(path, "http://localhost").pathname.endsWith("/metadata"),
    ),
  ).toBe(false);
  expect(
    f.fetch.mock.calls.every(
      (call) =>
        call.length === 1 || !call[1]?.method || call[1]?.method === "GET",
    ),
  ).toBe(true);
});

it("never treats delivered as proof of physical handover", async () => {
  const f = setup();
  await open();
  quantity();
  fireEvent.change(screen.getByLabelText("Документ"), {
    target: { value: "demand" },
  });
  expect(screen.getByLabelText("Фактическое время события")).toHaveValue("");
  expect(previewButton()).toBeDisabled();
  fillEvent();
  expect(previewButton()).toBeDisabled();
  fireEvent.click(
    screen.getByLabelText(/Подтверждаю: выбранный товар фактически передан/),
  );
  expect(previewButton()).not.toBeDisabled();
  fireEvent.click(previewButton());
  expect(f.onPreview).toHaveBeenCalledWith(
    expect.objectContaining({
      action: "demand_create",
      applicable: { presence: "value", value: true },
      event_kind: "shipment_dispatched",
      event_confirmed: true,
    }),
  );
  expect(f.onPreview.mock.calls[0][0].lines[0].identity.sku).toBe(
    "9007199254740993",
  );
  expect(f.onDraftChange).toHaveBeenCalled();
});

function demand(id: string, overrides: Record<string, unknown> = {}) {
  return {
    id,
    scope: orderScopeBody(scope),
    state: "RECONCILED",
    command: {
      scope: orderScopeBody(scope),
      action: "demand_create",
      posting_number: posting.posting_number,
      native_movement: {
        applicable: { presence: "value", value: true },
        correlation: { kind: "demand" },
      },
    },
    receipt: { external_id: "00000000-0000-4000-8000-000000000001" },
    ...overrides,
  };
}
function history(targets: unknown[], next = "") {
  return {
    operations: [
      {
        scope: orderScopeBody(scope),
        created_at: new Date().toISOString(),
        targets,
      },
    ],
    next_cursor: next,
  };
}

it("previews an explicitly confirmed rejected event without revealing or replacing its reference", async () => {
  const rejected = demand("rejected-event", {
    state: "REJECTED",
    dispatch_count: 1,
    command: {
      scope: orderScopeBody(scope),
      action: "demand_create",
      posting_number: posting.posting_number,
      native_movement: {
        applicable: { presence: "value", value: true },
        correlation: { kind: "demand" },
        event: {
          kind: "shipment_dispatched",
          occurred_at: new Date(Date.now() - 5000).toISOString(),
          confirmed: true,
        },
        moment: new Date().toISOString(),
        lines: [{ identity: posting.lines[0].identity, quantity: "1" }],
      },
    },
    receipt: { state: "REJECTED", http_status: 403, code: "PROVIDER_REJECTED" },
  });
  const f = setup({
    history: history([
      rejected,
      { ...rejected, id: "unknown", state: "UNKNOWN" },
      {
        ...rejected,
        id: "conflict",
        receipt: { state: "REJECTED", http_status: 409 },
      },
    ]),
  });
  await open();
  fireEvent.click(screen.getByLabelText("Подготовить повтор после отказа API"));
  await screen.findByRole("option", { name: /HTTP 403/ });
  expect(screen.getAllByRole("option", { name: /HTTP/ })).toHaveLength(1);
  fireEvent.change(
    screen.getByLabelText("Операция для проверки повторной попытки"),
    { target: { value: "rejected-event" } },
  );
  expect(screen.getByLabelText("Документ")).toBeDisabled();
  expect(screen.getByLabelText("Фактическое время события")).toBeDisabled();
  expect(
    screen.queryByLabelText("Идентификатор реального события"),
  ).not.toBeInTheDocument();
  expect(previewButton()).toBeDisabled();
  fireEvent.click(
    screen.getByLabelText(/Подтверждаю: выбранный товар фактически передан/),
  );
  expect(previewButton()).not.toBeDisabled();
  fireEvent.click(previewButton());
  expect(f.onPreview).toHaveBeenCalledWith(
    expect.objectContaining({
      retry_work_id: "rejected-event",
      event_confirmed: true,
      lines: [{ identity: posting.lines[0].identity, quantity: "1" }],
    }),
  );
  expect(f.onPreview.mock.calls[0][0]).not.toHaveProperty("event_reference");
  expect(f.onPreview.mock.calls[0][0]).not.toHaveProperty("event_occurred_at");
  expect(
    f.fetch.mock.calls.every(
      (call) => !call[1]?.method || call[1].method === "GET",
    ),
  ).toBe(true);
});

it("allows only a verified posted demand for the exact posting as return basis", async () => {
  const f = setup({
    history: history([
      demand("right"),
      demand("unknown", { state: "UNKNOWN" }),
      demand("foreign", {
        scope: { ...orderScopeBody(scope), tenant: "other" },
      }),
      demand("draft", {
        command: {
          scope: orderScopeBody(scope),
          action: "demand_create",
          posting_number: posting.posting_number,
          native_movement: {
            applicable: { presence: "zero", value: false },
            correlation: { kind: "demand" },
          },
        },
      }),
    ]),
  });
  await open();
  quantity();
  fireEvent.change(screen.getByLabelText("Документ"), {
    target: { value: "return" },
  });
  await screen.findByRole("option", { name: /Отгрузка от/ });
  expect(screen.getAllByRole("option", { name: /Отгрузка от/ })).toHaveLength(
    1,
  );
  fillEvent();
  fireEvent.click(
    screen.getByLabelText(/Подтверждаю: выбранный товар фактически получен/),
  );
  expect(previewButton()).toBeDisabled();
  fireEvent.change(screen.getByLabelText("Отгрузка этого отправления"), {
    target: { value: "right" },
  });
  expect(previewButton()).not.toBeDisabled();
  fireEvent.click(previewButton());
  expect(f.onPreview).toHaveBeenCalledWith(
    expect.objectContaining({
      action: "salesreturn_create",
      demand_work_id: "right",
      event_kind: "return_received",
    }),
  );
});

it("paginates return history with a bounded exact-posting query and clears selection", async () => {
  const f = setup({ history: history([demand("right")], "next-page") });
  await open();
  fireEvent.change(screen.getByLabelText("Документ"), {
    target: { value: "return" },
  });
  await screen.findByRole("option", { name: /Отгрузка от/ });
  fireEvent.change(screen.getByLabelText("Отгрузка этого отправления"), {
    target: { value: "right" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Ещё отгрузки" }));
  await waitFor(() =>
    expect(
      f.fetch.mock.calls.some(
        ([path]) =>
          new URL(path, "http://localhost").searchParams.get("cursor") ===
          "next-page",
      ),
    ).toBe(true),
  );
  expect(screen.getByLabelText("Отгрузка этого отправления")).toHaveValue("");
  for (const [path] of f.fetch.mock.calls) {
    const url = new URL(path, "http://localhost");
    if (url.pathname.endsWith("/operations")) {
      expect(url.searchParams.get("search")).toBe(posting.posting_number);
      expect(url.searchParams.get("limit")).toBe("25");
    }
  }
});

it("refuses foreign configuration and posting scopes", async () => {
  const f = setup();
  f.view.rerender(
    <QueryClientProvider client={f.client}>
      <OrdersNativeDocuments
        {...f.props}
        posting={{ ...posting, scope: { ...posting.scope, tenant: "other" } }}
      />
    </QueryClientProvider>,
  );
  expect(screen.getByRole("alert").textContent).toContain(
    "другому подключению",
  );
  expect(f.fetch).not.toHaveBeenCalled();
});

it("requires a live posted-document policy and resets private form state on account changes", async () => {
  const f = setup({
    nativePolicy: {
      ...policy(),
      valid_until: new Date(Date.now() - 1000).toISOString(),
    },
  });
  await open();
  quantity();
  fireEvent.change(screen.getByLabelText("Документ"), {
    target: { value: "demand" },
  });
  fillEvent();
  fireEvent.click(
    screen.getByLabelText(/Подтверждаю: выбранный товар фактически передан/),
  );
  expect(previewButton()).toBeDisabled();
  f.view.rerender(
    <QueryClientProvider client={f.client}>
      <OrdersNativeDocuments
        {...f.props}
        posting={{
          ...posting,
          evidence: { ...posting.evidence, revision: "source-2" },
        }}
      />
    </QueryClientProvider>,
  );
  const details = screen
    .getByText("Отгрузка и возврат в МойСклад")
    .closest("details");
  expect(details?.open).toBe(false);
  expect(screen.getByLabelText("Идентификатор планирования")).toHaveValue("");
  expect(screen.getByLabelText(/Количество · Тестовый товар/)).toHaveValue("");
});

it("uses independently verified native history after a lost acknowledgment", async () => {
  const f = setup({
    history: history([
      demand("verified-lost-response", {
        receipt: {},
        native_document: {
          id: "00000000-0000-4000-8000-000000000009",
          kind: "demand",
          observed_at: new Date().toISOString(),
        },
      }),
    ]),
  });
  await open();
  quantity();
  fireEvent.change(screen.getByLabelText("Документ"), {
    target: { value: "return" },
  });
  await screen.findByRole("option", {
    name: /00000000-0000-4000-8000-000000000009/,
  });
  fillEvent();
  fireEvent.click(
    screen.getByLabelText(/Подтверждаю: выбранный товар фактически получен/),
  );
  fireEvent.change(screen.getByLabelText("Отгрузка этого отправления"), {
    target: { value: "verified-lost-response" },
  });
  fireEvent.click(previewButton());
  expect(f.onPreview).toHaveBeenCalledWith(
    expect.objectContaining({ demand_work_id: "verified-lost-response" }),
  );
});

it("rejects a foreign configuration without enabling document creation", async () => {
  const f = setup();
  f.fetch.mockImplementationOnce(async () =>
    response({
      scope: { ...orderScopeBody(scope), tenant: "other" },
      enabled: true,
      test_enabled: true,
      publication_available: true,
      native_documents: policy(),
    }),
  );
  const details = screen
    .getByText("Отгрузка и возврат в МойСклад")
    .closest("details");
  if (!details) throw new Error("missing form");
  details.open = true;
  fireEvent(details, new Event("toggle"));
  await screen.findByRole("alert");
  expect(previewButton()).toBeDisabled();
  expect(f.onPreview).not.toHaveBeenCalled();
});

it("checks the aggregate document quantity with exact decimals", () => {
  expect(
    nativeQuantityTotalAtMost(
      ["1.000000000000000001", "1.999999999999999999"],
      "3",
    ),
  ).toBe(true);
  expect(
    nativeQuantityTotalAtMost(
      ["1.000000000000000002", "1.999999999999999999"],
      "3",
    ),
  ).toBe(false);
  expect(nativeQuantityTotalAtMost(["6", "6"], "10")).toBe(false);
  expect(
    nativeQuantityTotalAtMost(
      ["9007199254740993", "0.000000000000000001"],
      "9007199254740993.000000000000000001",
    ),
  ).toBe(true);
  for (const values of [
    [],
    ["0"],
    ["-1"],
    ["NaN"],
    Array<string>(101).fill("1"),
  ])
    expect(nativeQuantityTotalAtMost(values, "1000")).toBe(false);
});

it("invalidates the preview when the real event ages out before the policy", async () => {
  const f = setup({ nativePolicy: { ...policy(), event_max_age_seconds: 2 } });
  await open();
  quantity();
  fireEvent.change(screen.getByLabelText("Документ"), {
    target: { value: "demand" },
  });
  vi.useFakeTimers();
  const date = new Date(Date.now() - 1000);
  const local = new Date(date.getTime() - date.getTimezoneOffset() * 60000)
    .toISOString()
    .slice(0, -1);
  fireEvent.change(screen.getByLabelText("Фактическое время события"), {
    target: { value: local },
  });
  fireEvent.change(screen.getByLabelText("Идентификатор реального события"), {
    target: { value: "real-event-reference" },
  });
  fireEvent.click(
    screen.getByLabelText(/Подтверждаю: выбранный товар фактически передан/),
  );
  expect(previewButton()).not.toBeDisabled();
  f.onDraftChange.mockClear();
  await act(async () => vi.advanceTimersByTime(1001));
  expect(previewButton()).toBeDisabled();
  expect(f.onDraftChange).toHaveBeenCalledTimes(1);
  await act(async () => vi.advanceTimersByTime(1000));
  expect(f.onDraftChange).toHaveBeenCalledTimes(1);
});

it("blocks a document whose independently valid line quantities exceed the total limit", async () => {
  const f = setup();
  f.view.rerender(
    <QueryClientProvider client={f.client}>
      <OrdersNativeDocuments
        {...f.props}
        posting={{
          ...posting,
          lines: [
            {
              ...posting.lines[0],
              quantity: "6",
              name: { presence: "value", value: "Товар А" },
            },
            {
              ...posting.lines[0],
              identity: { provider_line_id: "line-2", sku: "456" },
              quantity: "6",
              name: { presence: "value", value: "Товар Б" },
            },
          ],
        }}
      />
    </QueryClientProvider>,
  );
  await open();
  fireEvent.change(screen.getByLabelText(/Количество · Товар А/), {
    target: { value: "6" },
  });
  fireEvent.change(screen.getByLabelText(/Количество · Товар Б/), {
    target: { value: "6" },
  });
  fireEvent.change(screen.getByLabelText("Плановое время документа"), {
    target: { value: "2026-12-01T12:34" },
  });
  fireEvent.change(screen.getByLabelText("Идентификатор планирования"), {
    target: { value: "synthetic-plan" },
  });
  expect(previewButton()).toBeDisabled();
  expect(screen.getByText(/Суммарное количество должно/)).toBeInTheDocument();
  fireEvent.change(screen.getByLabelText(/Количество · Товар Б/), {
    target: { value: "4" },
  });
  expect(previewButton()).not.toBeDisabled();
});
