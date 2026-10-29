import { expect, test, type Page, type Route } from "@playwright/test";

const origin = process.env.STYK_ORDERS_UI_ORIGIN || "http://127.0.0.1:5173";
const scope = {
  tenant: "synthetic-orders",
  moysklad_account: "synthetic-moy",
  ozon_account: "synthetic-ozon",
};
const known = (value: unknown) => ({ presence: "value", value });
const missing = { presence: "missing" };
const posting = (number: string) => ({
  scope,
  commercial_order_id: "ORDER-SYNTHETIC",
  posting_number: number,
  parent_posting_number:
    number === "SYNTHETIC-2" ? known("SYNTHETIC-1") : missing,
  child_posting_numbers: number === "SYNTHETIC-1" ? ["SYNTHETIC-2"] : [],
  order_number: known("ORDER-SYNTHETIC"),
  status: known("awaiting_packaging"),
  substatus: missing,
  scheme: known("FBS"),
  warehouse_id: known("Склад Ozon для теста"),
  financial: {
    order_total:
      number === "SYNTHETIC-1"
        ? { presence: "zero", value: { amount: "0", currency: "RUB" } }
        : missing,
  },
  times: { created_at: known("2026-10-29T12:00:00Z") },
  lines: [
    {
      identity: { offer_id: "SYNTHETIC-SKU", sku: "123456" },
      quantity: "0",
      name: known("Тестовый товар"),
      price: known({ amount: "0", currency: "RUB" }),
    },
  ],
  evidence: {
    provider_updated_at: missing,
    observed_at: "2026-10-29T12:00:00Z",
    checked_at: "2026-10-29T12:01:00Z",
    revision: "synthetic-v1",
  },
  drift: [],
});
const config = {
  scope,
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
  field_mappings: [
    { source: "posting_number", target: "name" },
    { source: "positions.quantity", target: "positions.quantity" },
  ],
  status_mappings: [],
};
const reference = (id: string, name: string) => ({ id, name });
const metadata = {
  scope,
  organizations: [reference("org", "Тестовая организация")],
  agents: [reference("agent", "Контрагент Ozon")],
  stores: [reference("store", "Основной склад")],
  states: [reference("state", "Новый заказ")],
  sales_channels: [reference("channel", "Ozon")],
  attributes: [],
  currencies: [{ id: "currency", iso_code: "RUB", default: true }],
};
function decision(number: string) {
  const ready = number === "SYNTHETIC-1";
  return {
    posting: posting(number),
    correlation: {},
    ready,
    validation: ready
      ? []
      : [{ field: "evidence.checked_at", code: "source_stale" }],
    commands: ready
      ? [
          {
            id: "command-synthetic",
            action: "mirror_create",
            posting_number: number,
            correlation: {},
            customer_order: {
              organization_id: "org",
              agent_id: "agent",
              store_id: "store",
              state_id: "state",
              sales_channel_id: "channel",
              currency_id: "currency",
              name: known(number),
              description: missing,
              moment: known("2026-10-29T12:00:00Z"),
              applicable: known(false),
              positions: [
                {
                  identity: { offer_id: "SYNTHETIC-SKU" },
                  quantity: "0",
                  price: known({ amount: "0", currency: "RUB" }),
                  reserve: { presence: "zero", value: "0" },
                  mapping: {
                    moysklad_id: "product-synthetic",
                    moysklad_kind: "product",
                  },
                },
              ],
            },
          },
        ]
      : [],
  };
}
const fulfill = (route: Route, body: unknown) =>
  route.fulfill({
    status: 200,
    contentType: "application/json",
    body: JSON.stringify(body),
  });

async function openOrders(page: Page, lostApproval = false) {
  const approvals: unknown[] = [];
  const errors: string[] = [];
  let submitted = false;
  page.on("pageerror", (error) => errors.push(error.message));
  const source = await (
    await page.request.get(`${origin}/src/Orders.tsx`)
  ).text();
  const queryModule = source.match(
    /from "([^"\n]*@tanstack_react-query\.js[^"\n]*)"/,
  )?.[1];
  if (!queryModule)
    throw new Error(
      "Orders browser tests require the local Vite development server",
    );
  await page.route("**/*", (route) =>
    ["127.0.0.1", "localhost"].includes(new URL(route.request().url()).hostname)
      ? route.continue()
      : route.abort(),
  );
  await page.route("**/api/orders/**", (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (url.pathname.endsWith("/config")) return fulfill(route, config);
    if (url.pathname.endsWith("/metadata")) return fulfill(route, metadata);
    if (url.pathname.endsWith("/metadata/registry"))
      return fulfill(route, { scope, revision: 0, attributes: [] });
    if (url.pathname.endsWith("/postings"))
      return fulfill(
        route,
        url.searchParams.get("cursor") === "page-2"
          ? { postings: [posting("SYNTHETIC-2")] }
          : { postings: [posting("SYNTHETIC-1")], next_cursor: "page-2" },
      );
    if (url.pathname.endsWith("/preview")) {
      const body = request.postDataJSON();
      expect(body.expected_revision).toBe(1);
      expect(body.tenant).toBe(scope.tenant);
      return fulfill(route, {
        id: "preview-synthetic",
        scope,
        config_revision: 1,
        expires_at: new Date(Date.now() + 60000).toISOString(),
        decisions: body.posting_numbers.map(decision),
      });
    }
    if (url.pathname.endsWith("/approve")) {
      approvals.push(request.postDataJSON());
      submitted = true;
      return lostApproval
        ? route.abort("failed")
        : fulfill(route, {
            id: "operation-synthetic",
            scope,
            state: "QUEUED",
            targets: [],
          });
    }
    if (url.pathname.endsWith("/operations"))
      return fulfill(route, {
        operations: submitted
          ? [
              {
                id: "operation-synthetic",
                scope,
                state: lostApproval ? "UNKNOWN" : "ACKNOWLEDGED",
                created_at: "2026-10-29T12:00:00Z",
                targets: [
                  {
                    id: "work-synthetic",
                    state: lostApproval ? "UNKNOWN" : "ACKNOWLEDGED",
                    command: decision("SYNTHETIC-1").commands[0],
                    automatic: false,
                    receipt: {
                      state: "ACKNOWLEDGED",
                      external_id: "order-moy-synthetic",
                    },
                  },
                ],
              },
            ]
          : [],
      });
    throw new Error(`Unexpected synthetic Orders endpoint: ${url.pathname}`);
  });
  await page.route("**/__orders-ui", (route) =>
    route.fulfill({
      contentType: "text/html",
      body: `<!doctype html><html lang="ru"><head><meta charset="UTF-8"/><meta name="viewport" content="width=device-width, initial-scale=1"/><title>STYK: заказы</title></head><body class="reference-shell"><main style="max-width:1100px;margin:24px auto;padding:16px"><div id="orders-root"></div></main><script type="module">import RefreshRuntime from '/@react-refresh';RefreshRuntime.injectIntoGlobalHook(window);window.$RefreshReg$=()=>{};window.$RefreshSig$=()=>type=>type;window.__vite_plugin_react_preamble_installed__=true;</script><script type="module">import React from '/node_modules/.vite/deps/react.js';import ReactDOM from '/node_modules/.vite/deps/react-dom_client.js';import {QueryClient,QueryClientProvider} from '${queryModule}';import {Orders} from '/src/Orders.tsx';import '/src/style.css';ReactDOM.createRoot(document.getElementById('orders-root')).render(React.createElement(QueryClientProvider,{client:new QueryClient({defaultOptions:{queries:{retry:false}}})},React.createElement(Orders,{accountScope:{tenant:'synthetic-orders',moy:'synthetic-moy',ozon:'synthetic-ozon'},moyLabel:'Учёт товаров',ozonLabel:'Магазин'})));</script></body></html>`,
    }),
  );
  await page.goto(`${origin}/__orders-ui`);
  await expect(
    page.getByRole("heading", { name: "Заказы", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByLabel("Выбрать отправление SYNTHETIC-1"),
  ).toBeVisible();
  return { approvals, errors };
}

test("orders desktop/mobile review uses keyboard and only explicitly approves eligible postings", async ({
  page,
}, testInfo) => {
  await page.setViewportSize({ width: 1440, height: 1000 });
  const { approvals, errors } = await openOrders(page);
  const first = page.getByLabel("Выбрать отправление SYNTHETIC-1");
  await first.focus();
  await first.press("Space");
  await expect(first).toBeFocused();
  await expect(first).toBeChecked();
  await page.getByRole("button", { name: "Следующие отправления" }).click();
  await page.getByLabel("Выбрать отправление SYNTHETIC-2").check();
  await page
    .getByRole("button", { name: "Проверить выбранные отправления" })
    .click();
  await expect(
    page.getByLabel("Подтвердить отправление SYNTHETIC-2"),
  ).toBeDisabled();
  await expect(page.getByText(/Данные Ozon устарели/)).toBeVisible();
  expect(approvals).toHaveLength(0);
  await page.screenshot({
    path: testInfo.outputPath("orders-desktop.png"),
    fullPage: true,
  });
  await page.setViewportSize({ width: 390, height: 844 });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
  await page.screenshot({
    path: testInfo.outputPath("orders-mobile.png"),
    fullPage: true,
  });
  const approved = page.getByLabel("Подтвердить отправление SYNTHETIC-1");
  await approved.focus();
  await approved.press("Space");
  await expect(
    page.getByRole("button", { name: "Передать в МойСклад (1)" }),
  ).toBeDisabled();
  await page.getByLabel(/Я проверил состав и поля/).check();
  await page.getByRole("button", { name: "Передать в МойСклад (1)" }).click();
  await expect(page.getByText(/Подтверждение записано/)).toBeVisible();
  expect(approvals).toEqual([
    {
      ...scope,
      preview_id: "preview-synthetic",
      posting_numbers: ["SYNTHETIC-1"],
      idempotency_key: expect.any(String),
      acknowledge: true,
    },
  ]);
  await expect(
    page.getByText("Принято, ожидает сверки", { exact: true }).first(),
  ).toBeVisible();
  expect(await page.locator("body").innerText()).not.toMatch(
    /workspace|рабочее пространство/i,
  );
  expect(errors).toEqual([]);
});

test("unknown approval outcome shows read-only status refresh without another write", async ({
  page,
}) => {
  const { approvals, errors } = await openOrders(page, true);
  await page.getByLabel("Выбрать отправление SYNTHETIC-1").check();
  await page
    .getByRole("button", { name: "Проверить выбранные отправления" })
    .click();
  await page.getByLabel("Подтвердить отправление SYNTHETIC-1").check();
  await page.getByLabel(/Я проверил состав и поля/).check();
  await page.getByRole("button", { name: "Передать в МойСклад (1)" }).click();
  await expect(
    page.getByText(/Результат подтверждения неизвестен:/),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Передать в МойСклад (1)" }),
  ).toBeDisabled();
  await expect(page.getByText(/Запрос мог выполниться/)).toBeVisible();
  await page.getByRole("button", { name: "Обновить статусы заказов" }).click();
  expect(approvals).toHaveLength(1);
  expect(errors).toEqual([]);
});
