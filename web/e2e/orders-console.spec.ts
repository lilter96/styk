import { expect, test, type Page, type Route } from "@playwright/test";
import { readFile } from "node:fs/promises";
import { auditButtonHovers } from "./hover-contrast";
import type { OrderActionProposal, OrderCommand } from "../src/OrdersTypes";

const origin = process.env.STYK_ORDERS_UI_ORIGIN || "http://127.0.0.1:5173";

test("composition replacement has explicit preview and separate destructive approval", async ({
  page,
}, testInfo) => {
  const { approvals, errors } = await openOrders(page, false, true);
  const proposed = decision("SYNTHETIC-1");
  const command = proposed.commands[0];
  command.action = "mirror_composition_update";
  command.customer_order.positions[0].quantity = "2";
  const replacement = {
    confirm_replacement: true,
    retained: [],
    added: [{ offer_id: "SYNTHETIC-SKU", sku: "123456" }],
    removed: [
      {
        source_identity: { sku: "9007199254740993" },
        native_position_id: "native-old",
      },
    ],
    preserved: ["foreign-same-sku", "foreign-other-product"],
  };
  let previews = 0;
  await page.route("**/api/orders/preview", (route) => {
    const body = route.request().postDataJSON();
    expect(body.actions).toEqual(["mirror_composition_update"]);
    expect(body.composition_proposals).toEqual([
      { posting_number: "SYNTHETIC-1", confirm_replacement: true },
    ]);
    expect(body.proposals).toBeUndefined();
    expect(body.native_proposals).toBeUndefined();
    previews++;
    return fulfill(route, {
      id: "preview-synthetic",
      scope,
      config_revision: 1,
      expires_at: new Date(Date.now() + 300000).toISOString(),
      decisions: [
        {
          ...proposed,
          correlation: {
            customer_order_id: "11111111-1111-4111-8111-111111111111",
          },
          commands: [
            {
              ...command,
              correlation: {
                customer_order_id: "11111111-1111-4111-8111-111111111111",
              },
              position_replacement: replacement,
            },
          ],
        },
      ],
    });
  });
  await page
    .getByText("Изменить состав заказа МойСклад", { exact: true })
    .click();
  const review = page.getByRole("button", {
    name: "Проверить замену состава",
    exact: true,
  });
  await expect(review).toBeDisabled();
  await page
    .getByLabel(
      "Разрешаю подготовить замену состава этого заказа по текущему отправлению Ozon.",
    )
    .check();
  expect(approvals).toHaveLength(0);
  await review.click();
  await expect(
    page.getByRole("heading", { name: "Проверка замены состава заказа" }),
  ).toBeVisible();
  await expect(page.getByText("Удалить: 9007199254740993")).toBeVisible();
  await expect(
    page.locator(".orders-facts > div").filter({
      has: page.getByText("Чужие строки без изменений", { exact: true }),
    }),
  ).toHaveText("Чужие строки без изменений2");
  expect(previews).toBe(1);
  expect(approvals).toHaveLength(0);
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page
    .getByRole("heading", { name: "Проверка замены состава заказа" })
    .scrollIntoViewIfNeeded();
  await page
    .locator('section[aria-labelledby="orders-preview-heading"]')
    .screenshot({
      path: testInfo.outputPath("composition-desktop.png"),
    });
  await page.setViewportSize({ width: 390, height: 844 });
  await page
    .getByRole("heading", { name: "Проверка замены состава заказа" })
    .scrollIntoViewIfNeeded();
  expect(
    await page.evaluate(() => document.documentElement.scrollWidth),
  ).toBeLessThanOrEqual(390);
  await page
    .locator('section[aria-labelledby="orders-preview-heading"]')
    .screenshot({
      path: testInfo.outputPath("composition-mobile.png"),
    });
  await page.getByLabel("Подтвердить отправление SYNTHETIC-1").check();
  await page
    .getByLabel(
      "Я проверил добавляемые и удаляемые позиции и подтверждаю замену состава этого заказа МойСклад.",
    )
    .check();
  await page
    .getByRole("button", { name: "Заменить состав заказов (1)", exact: true })
    .click();
  await expect.poll(() => approvals.length).toBe(1);
  expect(errors).toEqual([]);
});

test("reserve policy is explicit and separate from document posting on desktop and mobile", async ({
  page,
}, testInfo) => {
  const { errors } = await openOrders(page);
  const saved: { config: typeof config }[] = [];
  await page.route("**/api/orders/config", (route) => {
    if (route.request().method() !== "POST") return route.fallback();
    const body = route.request().postDataJSON();
    saved.push(body);
    return fulfill(route, { ...body.config, scope, revision: 2 });
  });
  await page.getByRole("button", { name: "Настройки", exact: true }).click();
  await expect(page.getByLabel("Резерв в МойСклад")).toHaveValue("");
  await expect(page.getByLabel("Проведение заказа МойСклад")).toHaveValue("");
  await page
    .getByLabel("Резерв в МойСклад")
    .selectOption("posting.open_quantity");
  await expect(page.getByLabel("Проведение заказа МойСклад")).toHaveValue("");
  await expect(
    page.getByText(/Резерв будет равен количеству только/),
  ).toBeVisible();
  await page
    .getByLabel("Проведение заказа МойСклад")
    .selectOption("posting.cancelled");
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.getByLabel("Резерв в МойСклад").scrollIntoViewIfNeeded();
  await page.screenshot({
    path: testInfo.outputPath("reservation-settings-desktop.png"),
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByLabel("Резерв в МойСклад").scrollIntoViewIfNeeded();
  expect(
    await page.evaluate(() => document.documentElement.scrollWidth),
  ).toBeLessThanOrEqual(390);
  await page.screenshot({
    path: testInfo.outputPath("reservation-settings-mobile.png"),
  });
  await page
    .getByRole("button", { name: "Сохранить настройки заказов", exact: true })
    .click();
  await expect.poll(() => saved.length).toBe(1);
  expect(saved[0].config.field_mappings).toContainEqual({
    source: "posting.open_quantity",
    target: "positions.reserve",
  });
  expect(saved[0].config.field_mappings).toContainEqual({
    source: "posting.cancelled",
    target: "applicable",
  });
  expect(saved[0].config.field_mappings).toContainEqual(
    config.field_mappings[0],
  );
  expect(errors).toEqual([]);
});

test("label download appears after reconciliation and expires without another create", async ({
  page,
}, testInfo) => {
  const now = new Date("2026-11-04T10:00:00Z");
  await page.clock.install({ time: now });
  const { approvals } = await openOrders(page);
  let state = "POLLING";
  const expires = new Date(now.getTime() + 86400000).toISOString();
  let operationReads = 0;
  await page.route("**/api/orders/operations?*", (route) => {
    operationReads++;
    return fulfill(route, {
      operations: [
        {
          id: "label-operation",
          scope,
          state,
          created_at: new Date().toISOString(),
          targets: [
            {
              id: "label-work",
              state,
              automatic: false,
              command: {
                action: "posting_label_create",
                posting_number: "SYNTHETIC-1",
                correlation: {},
              },
              receipt: {
                state,
                task_ids: [{ id: "9007199254740993", type: "big_label" }],
                artifact: {
                  available: true,
                  download_url: "/api/orders/artifacts/label-work",
                  label_type: "big_label",
                  expires_at: expires,
                },
              },
            },
          ],
        },
      ],
    });
  });
  await page.route("**/api/orders/artifacts/label-work", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/pdf",
      headers: {
        "Content-Disposition": 'attachment; filename="ozon-label.pdf"',
      },
      body: "%PDF-1.7\nSYNTHETIC\n%%EOF",
    }),
  );
  await showHistory(page);
  await page
    .getByRole("button", { name: "Обновить статусы", exact: true })
    .click();
  await expect(
    page.getByRole("link", { name: "Скачать этикетку" }),
  ).toHaveCount(0);
  state = "RECONCILED";
  await showHistory(page);
  await page
    .getByRole("button", { name: "Обновить статусы", exact: true })
    .click();
  await openHistoryOperation(page);
  await expect(
    page.getByRole("link", { name: "Скачать этикетку" }),
  ).toBeVisible();
  await page.screenshot({
    path: testInfo.outputPath("label-desktop.png"),
    fullPage: true,
  });
  await page.setViewportSize({ width: 390, height: 844 });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
  await page.screenshot({
    path: testInfo.outputPath("label-mobile.png"),
    fullPage: true,
  });
  const downloading = page.waitForEvent("download");
  await page.getByRole("link", { name: "Скачать этикетку" }).click();
  const download = await downloading;
  expect(download.suggestedFilename()).toBe("ozon-label.pdf");
  expect(await download.failure()).toBeNull();
  const downloadedPath = await download.path();
  if (!downloadedPath) throw new Error("Synthetic PDF was not saved");
  expect((await readFile(downloadedPath)).toString()).toBe(
    "%PDF-1.7\nSYNTHETIC\n%%EOF",
  );
  const readsBeforeExpiry = operationReads;
  await page.clock.fastForward(86400001);
  await expect(
    page.getByRole("link", { name: "Скачать этикетку" }),
  ).toHaveCount(0);
  await expect(page.getByText("Срок хранения этикетки истёк.")).toBeVisible();
  expect(approvals).toHaveLength(0);
  expect(operationReads).toBe(readsBeforeExpiry);
});

test("split history shows exact child identities and partial readback on desktop and mobile", async ({
  page,
}, testInfo) => {
  await openOrders(page);
  await page.route("**/api/orders/operations?*", (route) =>
    fulfill(route, {
      operations: [
        {
          id: "split-operation",
          scope,
          state: "POLLING",
          created_at: "2026-11-04T10:00:00Z",
          targets: [
            {
              id: "split-work",
              state: "POLLING",
              automatic: false,
              command: {
                action: "posting_split",
                posting_number: "SYNTHETIC-1",
                correlation: {},
              },
              receipt: {
                state: "POLLING",
                posting_numbers: ["child-9007199254740993", "child-b"],
                observed_postings: [
                  {
                    posting_number: "child-9007199254740993",
                    evidence: { observed_at: "2026-11-04T10:00:00Z" },
                  },
                ],
              },
            },
          ],
        },
      ],
    }),
  );
  await showHistory(page);
  await page
    .getByRole("button", { name: "Обновить статусы", exact: true })
    .click();
  await openHistoryOperation(page);
  await page.getByText("Технические сведения", { exact: true }).click();
  const summary = page.getByText("Посылки после разделения (2)");
  await summary.focus();
  await summary.press("Enter");
  await expect(
    page.getByText("child-9007199254740993", { exact: true }),
  ).toBeVisible();
  await expect(page.getByText("Ожидаем подтверждения состава")).toBeVisible();
  await page.screenshot({
    path: testInfo.outputPath("split-desktop.png"),
    fullPage: true,
  });
  await page.setViewportSize({ width: 390, height: 844 });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
  await page.screenshot({
    path: testInfo.outputPath("split-mobile.png"),
    fullPage: true,
  });
});
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
    body: JSON.stringify(body, (key, value: unknown) => {
      if (key !== "targets" || !Array.isArray(value)) return value;
      return value.map((work: Record<string, unknown>) => ({
        scope,
        ...work,
        command: { scope, ...(work.command as Record<string, unknown>) },
      }));
    }),
  });

async function openOrders(
  page: Page,
  lostApproval = false,
  actionMode = false,
  withShell = false,
) {
  const approvals: unknown[] = [];
  const previews: { proposals?: OrderActionProposal[] }[] = [];
  let outgoing: OrderCommand | undefined;
  const currentPosting = (number: string) =>
    actionMode
      ? {
          ...posting(number),
          scheme: known("rFBS"),
          lines: posting(number).lines.map((line) => ({
            ...line,
            quantity: "2",
          })),
          evidence: {
            ...posting(number).evidence,
            checked_at: new Date().toISOString(),
          },
        }
      : posting(number);
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
  if (withShell) {
    await page.route("**/api/**", (route) => {
      const path = new URL(route.request().url()).pathname;
      if (path === "/api/session")
        return fulfill(route, {
          user: { id: "synthetic-user", email: "seller@example.test" },
          account_id: scope.tenant,
          csrf: "synthetic-csrf",
          expires_at: "2099-01-01T00:00:00Z",
        });
      if (path === "/api/registrations")
        return fulfill(route, {
          accounts: [
            {
              source: "moysklad",
              account: scope.moysklad_account,
              name: "Учёт товаров",
              registered_at: "2026-01-01",
            },
            {
              source: "ozon",
              account: scope.ozon_account,
              name: "Магазин",
              registered_at: "2026-01-01",
            },
          ],
          truncated: false,
        });
      if (path === "/api/diagnostics")
        return fulfill(route, { checks: [], batches: {}, mapping_counts: {} });
      if (path === "/api/integrations")
        return fulfill(route, {
          providers: [
            {
              provider: "moysklad",
              status: "VERIFIED",
              registration: "REGISTERED",
              credentials_stored: true,
            },
          ],
        });
      return route.fulfill({
        status: 404,
        json: { error: "FIXTURE_ENDPOINT_UNAVAILABLE" },
      });
    });
  }
  await page.route("**/api/orders/**", (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (url.pathname.endsWith("/config")) return fulfill(route, config);
    if (url.pathname.endsWith("/metadata")) return fulfill(route, metadata);
    if (url.pathname.endsWith("/metadata/registry"))
      return fulfill(route, { scope, revision: 0, attributes: [] });
    if (url.pathname.endsWith("/action-options"))
      return fulfill(route, {
        scope,
        posting_number: "SYNTHETIC-1",
        source_revision: "synthetic-v1",
        actions: [
          "posting_pack",
          "posting_deliver",
          "posting_tracking",
          "posting_label_create",
        ].map((action) => ({
          action,
          allowed: true,
          blockers: [],
          parameters_required: [],
        })),
        cancellation_reasons: [],
        label_types: ["big_label", "small_label"],
      });
    if (url.pathname.endsWith("/postings"))
      return fulfill(
        route,
        url.searchParams.get("cursor") === "page-2"
          ? { postings: [currentPosting("SYNTHETIC-2")] }
          : {
              postings: [currentPosting("SYNTHETIC-1")],
              next_cursor: "page-2",
            },
      );
    if (url.pathname.endsWith("/preview")) {
      const body = request.postDataJSON();
      previews.push(body);
      expect(body.expected_revision).toBe(1);
      expect(body.tenant).toBe(scope.tenant);
      if (body.proposals?.length) {
        const proposal = body.proposals[0] as OrderActionProposal;
        const parameters = { ...proposal.parameters };
        if (parameters.country_iso_code) {
          delete parameters.country_iso_code;
          parameters.country_provided = true;
        }
        if (parameters.exemplars) {
          delete parameters.exemplars;
          parameters.exemplars_provided = true;
        }
        if (parameters.tracking_number) {
          delete parameters.tracking_number;
          parameters.tracking_provided = true;
        }
        if (parameters.event_reference) {
          delete parameters.event_reference;
          parameters.event_reference_provided = true;
        }
        if (parameters.reason_message) {
          delete parameters.reason_message;
          parameters.reason_message_provided = true;
        }
        outgoing = {
          id: "command-action-synthetic",
          action: proposal.action,
          posting_number: proposal.posting_number,
          correlation: {},
          posting_action: { parameters },
        };
        return fulfill(route, {
          id: "preview-synthetic",
          scope,
          config_revision: 1,
          expires_at: new Date(Date.now() + 60000).toISOString(),
          decisions: [
            {
              posting: currentPosting(proposal.posting_number),
              ready: true,
              correlation: {},
              validation: [],
              commands: [outgoing],
            },
          ],
        });
      }
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
                    command: outgoing ?? decision("SYNTHETIC-1").commands[0],
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
    if (url.pathname.endsWith("/reverse-suggestion"))
      return route.fulfill({
        status: 404,
        contentType: "application/json",
        body: JSON.stringify({ code: "NOT_FOUND" }),
      });
    throw new Error(`Unexpected synthetic Orders endpoint: ${url.pathname}`);
  });
  await page.route("**/__orders-ui", (route) =>
    route.fulfill({
      contentType: "text/html",
      body: `<!doctype html><html lang="ru"><head><meta charset="UTF-8"/><meta name="viewport" content="width=device-width, initial-scale=1"/><title>STYK: заказы</title></head><body class="reference-shell"><main style="max-width:1100px;margin:24px auto;padding:16px"><div id="orders-root"></div></main><script type="module">import RefreshRuntime from '/@react-refresh';RefreshRuntime.injectIntoGlobalHook(window);window.$RefreshReg$=()=>{};window.$RefreshSig$=()=>type=>type;window.__vite_plugin_react_preamble_installed__=true;</script><script type="module">import React from '/node_modules/.vite/deps/react.js';import ReactDOM from '/node_modules/.vite/deps/react-dom_client.js';import {QueryClient,QueryClientProvider} from '${queryModule}';import {Orders} from '/src/Orders.tsx';import '/src/style.css';import '/src/design-system.css';ReactDOM.createRoot(document.getElementById('orders-root')).render(React.createElement(QueryClientProvider,{client:new QueryClient({defaultOptions:{queries:{retry:false}}})},React.createElement(Orders,{accountScope:{tenant:'synthetic-orders',moy:'synthetic-moy',ozon:'synthetic-ozon'},moyLabel:'Учёт товаров',ozonLabel:'Магазин'})));</script></body></html>`,
    }),
  );
  await page.goto(`${origin}/${withShell ? "orders" : "__orders-ui"}`);
  await expect(
    page.getByRole("heading", { name: "Заказы", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByLabel("Выбрать отправление SYNTHETIC-1"),
  ).toBeVisible();
  if (actionMode) {
    await page
      .getByRole("button", {
        name: "Открыть отправление SYNTHETIC-1",
        exact: true,
      })
      .click();
    await page.getByRole("tab", { name: "Действия", exact: true }).click();
  }
  return { approvals, errors, previews };
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
  await showHistory(page);
  await expect(
    page
      .locator(".history-status")
      .filter({ hasText: "Ждём подтверждения" })
      .first(),
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
  await showHistory(page);
  await openHistoryOperation(page);
  await expect(page.getByText(/Запрос мог выполниться/)).toBeVisible();
  await showHistory(page);
  await page
    .getByRole("button", { name: "Обновить статусы", exact: true })
    .click();
  expect(approvals).toHaveLength(1);
  expect(errors).toEqual([]);
});

test("rFBS event requires actual-event reference, keyboard confirmation, preview and separate Ozon approval", async ({
  page,
}, testInfo) => {
  await page.setViewportSize({ width: 1440, height: 1000 });
  const { approvals, previews, errors } = await openOrders(page, false, true);
  await page
    .getByText("Действия с отправлением в Ozon", { exact: true })
    .click();
  await page
    .getByLabel("Действие для отправления SYNTHETIC-1")
    .selectOption("posting_deliver");
  await expect(
    page.getByRole("button", { name: "Проверить действие в Ozon" }),
  ).toBeDisabled();
  await page
    .getByLabel("Основание фактического события")
    .fill("ACT-SYNTHETIC-1");
  const actual = page.getByLabel(
    "Подтверждаю, что покупатель фактически получил отправление.",
  );
  await expect(actual).not.toBeChecked();
  await actual.focus();
  await actual.press("Space");
  await page.getByRole("button", { name: "Проверить действие в Ozon" }).click();
  await expect(
    page.getByRole("heading", { name: "Проверка действия в Ozon" }),
  ).toBeVisible();
  await expect(
    page.getByText("Основание фактического события введено; скрыто в журнале."),
  ).toBeVisible();
  expect(approvals).toHaveLength(0);
  expect(previews[0].proposals).toEqual([
    {
      posting_number: "SYNTHETIC-1",
      action: "posting_deliver",
      parameters: { event_reference: "ACT-SYNTHETIC-1", event_confirmed: true },
    },
  ]);
  await page.screenshot({
    path: testInfo.outputPath("orders-action-desktop.png"),
    fullPage: true,
  });
  await page.setViewportSize({ width: 390, height: 844 });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
  await page.screenshot({
    path: testInfo.outputPath("orders-action-mobile.png"),
    fullPage: true,
  });
  await page.getByLabel("Подтвердить отправление SYNTHETIC-1").check();
  await expect(
    page.getByRole("button", { name: "Подтвердить действие в Ozon (1)" }),
  ).toBeDisabled();
  await page.getByLabel(/Я проверил параметры выбранного действия/).check();
  await page
    .getByRole("button", { name: "Подтвердить действие в Ozon (1)" })
    .click();
  await expect(page.getByText(/Подтверждение записано/)).toBeVisible();
  expect(approvals).toHaveLength(1);
  expect(errors).toEqual([]);
});

test("package allocation on mobile has no default factual readiness and only explicit quantities enter preview", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  const { approvals, previews, errors } = await openOrders(page, false, true);
  await page
    .getByText("Действия с отправлением в Ozon", { exact: true })
    .click();
  await page
    .getByLabel("Действие для отправления SYNTHETIC-1")
    .selectOption("posting_pack");
  await page.getByLabel("Количество SYNTHETIC-SKU в упаковке 1").fill("1");
  await expect(
    page.getByRole("button", { name: "Проверить действие в Ozon" }),
  ).toBeDisabled();
  await page.getByLabel("Количество SYNTHETIC-SKU в упаковке 1").fill("2");
  const ready = page.getByLabel(
    /Подтверждаю, что указанные товары фактически собраны/,
  );
  await expect(ready).not.toBeChecked();
  await ready.check();
  await page.getByRole("button", { name: "Проверить действие в Ozon" }).click();
  await expect(
    page.getByRole("heading", { name: "Проверка действия в Ozon" }),
  ).toBeVisible();
  expect(previews[0].proposals).toEqual([
    {
      posting_number: "SYNTHETIC-1",
      action: "posting_pack",
      parameters: {
        packages: [
          {
            lines: [
              {
                identity: { offer_id: "SYNTHETIC-SKU", sku: "123456" },
                quantity: "2",
              },
            ],
          },
        ],
        readiness_confirmed: true,
      },
    },
  ]);
  expect(approvals).toHaveLength(0);
  expect(errors).toEqual([]);
});

test("owned native position preview stays selectable and explains its exact scope", async ({
  page,
}, testInfo) => {
  const { approvals, errors } = await openOrders(page);
  await page.route("**/api/orders/preview", (route) => {
    const item = decision("SYNTHETIC-1");
    return fulfill(route, {
      id: "preview-synthetic",
      scope,
      config_revision: 1,
      expires_at: new Date(Date.now() + 60000).toISOString(),
      decisions: [
        {
          ...item,
          commands: [
            {
              ...item.commands[0],
              action: "mirror_position_update",
              position_patch: {
                position_id: "11111111-2222-4333-8444-555555555555",
                identity: { offer_id: "SYNTHETIC-SKU" },
                quantity: known("1"),
                reserve: missing,
                price: known({ amount: "19.99", currency: "RUB" }),
              },
            },
          ],
        },
      ],
    });
  });
  await page.getByLabel("Выбрать отправление SYNTHETIC-1").check();
  await page
    .getByRole("button", { name: "Проверить выбранные отправления" })
    .click();
  await expect(
    page.getByText("Точечное изменение позиции SYNTHETIC-SKU"),
  ).toBeVisible();
  await expect(page.getByText("Без изменения", { exact: true })).toBeVisible();
  await expect(
    page.getByText(/Другие строки и их резервы сохраняются/),
  ).toBeVisible();
  await page.getByLabel("Подтвердить отправление SYNTHETIC-1").check();
  const submit = page.getByRole("button", { name: "Передать в МойСклад (1)" });
  await expect(submit).toBeDisabled();
  await page.setViewportSize({ width: 390, height: 844 });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
  await page.screenshot({
    path: testInfo.outputPath("position-patch-mobile.png"),
    fullPage: true,
  });
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.screenshot({
    path: testInfo.outputPath("position-patch-desktop.png"),
    fullPage: true,
  });
  await page.getByLabel(/Я проверил состав и поля/).check();
  await submit.click();
  await expect(page.getByText(/Подтверждение записано/)).toBeVisible();
  expect(approvals).toHaveLength(1);
  expect(errors).toEqual([]);
});

test("hybrid handover clears an expired printed-label selection without another provider check", async ({
  page,
}, testInfo) => {
  const now = new Date();
  await page.clock.install({ time: now });
  const { approvals, previews } = await openOrders(page, false, true);
  const expires = new Date(now.getTime() + 10000).toISOString();
  let operationReads = 0;
  await page.route("**/api/orders/operations?*", (route) => {
    operationReads++;
    return fulfill(route, {
      operations: [
        {
          id: "label-op",
          scope,
          state: "RECONCILED",
          created_at: now.toISOString(),
          targets: [
            {
              id: "0123456789abcdef0123456789abcdef",
              state: "RECONCILED",
              automatic: false,
              command: {
                id: "label-command",
                action: "posting_label_create",
                posting_number: "SYNTHETIC-1",
                correlation: {},
              },
              receipt: {
                state: "RECONCILED",
                artifact: {
                  available: true,
                  label_type: "big_label",
                  expires_at: expires,
                },
              },
            },
          ],
        },
      ],
    });
  });
  await page.route("**/api/orders/action-options?*", (route) =>
    fulfill(route, {
      scope,
      posting_number: "SYNTHETIC-1",
      source_revision: "synthetic-v1",
      actions: [
        {
          action: "posting_ship",
          allowed: true,
          blockers: [],
          parameters_required: [
            "event_confirmed",
            "event_reference",
            "label_work_id",
            "label_printed_confirmed",
          ],
        },
      ],
      label_types: ["big_label"],
      cancellation_reasons: [],
    }),
  );
  await page
    .getByText("Действия с отправлением в Ozon", { exact: true })
    .click();
  await page
    .getByLabel("Действие для отправления SYNTHETIC-1")
    .selectOption("posting_ship");
  await page.getByLabel("Основание фактического события").fill("ACT-SYNTHETIC");
  await page
    .getByLabel(
      "Подтверждаю, что отправление rFBS фактически передано в доставку.",
    )
    .check();
  const preview = page.getByRole("button", {
    name: "Проверить действие в Ozon",
  });
  await expect(preview).toBeDisabled();
  await page
    .getByLabel("Подтверждённая этикетка")
    .selectOption("0123456789abcdef0123456789abcdef");
  await expect(preview).toBeDisabled();
  await page
    .getByLabel("Этикетка распечатана и наклеена на это отправление.")
    .check();
  await expect(preview).toBeEnabled();
  await page.screenshot({
    path: testInfo.outputPath("hybrid-label-desktop.png"),
    fullPage: true,
  });
  await page.setViewportSize({ width: 390, height: 844 });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
  await page.screenshot({
    path: testInfo.outputPath("hybrid-label-mobile-ready.png"),
    fullPage: true,
  });
  const reads = operationReads;
  await page.clock.fastForward(11000);
  await expect(preview).toBeDisabled();
  await expect(page.getByLabel("Подтверждённая этикетка")).toHaveValue("");
  await expect(
    page.getByLabel("Этикетка распечатана и наклеена на это отправление."),
  ).not.toBeChecked();
  await page.setViewportSize({ width: 390, height: 844 });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
  await page.screenshot({
    path: testInfo.outputPath("hybrid-label-mobile-expired.png"),
    fullPage: true,
  });
  expect(operationReads).toBe(reads);
  expect(approvals).toHaveLength(0);
  expect(previews).toHaveLength(0);
});

test("exemplar edits require explicit input, preserve string IDs and use separate approval", async ({
  page,
}, testInfo) => {
  const { approvals, previews, errors } = await openOrders(page, false, true);
  await page.route("**/api/orders/action-options?*", (route) =>
    fulfill(route, {
      scope,
      posting_number: "SYNTHETIC-1",
      source_revision: "synthetic-v1",
      actions: [
        {
          action: "posting_exemplar_set",
          allowed: true,
          blockers: [],
          parameters_required: ["exemplar_work_id", "exemplars"],
        },
      ],
      cancellation_reasons: [],
      label_types: [],
    }),
  );
  await page.route("**/api/orders/exemplars?*", (route) =>
    fulfill(route, {
      scope,
      posting_number: "SYNTHETIC-1",
      source_revision: "synthetic-v1",
      work_id: "prepared-synthetic",
      target_fingerprint: "physical-fp",
      expires_at: new Date(Date.now() + 60000).toISOString(),
      products: [
        {
          sku: "9007199254740993",
          quantity: 1,
          ids: ["9007199254740995"],
          required: {
            mandatory_mark: known(true),
            imei: known(false),
            gtd: missing,
          },
        },
      ],
    }),
  );
  await page
    .getByText("Действия с отправлением в Ozon", { exact: true })
    .click();
  await page
    .getByLabel("Действие для отправления SYNTHETIC-1")
    .selectOption("posting_exemplar_set");
  const preview = page.getByRole("button", {
    name: "Проверить действие в Ozon",
  });
  await expect(preview).toBeDisabled();
  await expect(
    page.getByText(/Таможенная декларация: неизвестно/),
  ).toBeVisible();
  await page
    .getByLabel("Код маркировки", { exact: true })
    .fill("SCANNED-SYNTHETIC-CODE");
  await expect(preview).toBeEnabled();
  await page.screenshot({
    path: testInfo.outputPath("exemplars-desktop.png"),
    fullPage: true,
    mask: [page.getByLabel("Код маркировки", { exact: true })],
  });
  await page.setViewportSize({ width: 390, height: 844 });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
  await page.screenshot({
    path: testInfo.outputPath("exemplars-mobile.png"),
    fullPage: true,
    mask: [page.getByLabel("Код маркировки", { exact: true })],
  });
  await preview.click();
  expect(
    previews[0].proposals?.[0].parameters.exemplars?.products[0].exemplars[0]
      .id,
  ).toBe("9007199254740995");
  expect(approvals).toHaveLength(0);
  await expect(
    page.getByText(
      "Сведения экземпляров введены; маркировка и таможенные данные скрыты в журнале.",
    ),
  ).toBeVisible();
  expect(errors).toEqual([]);
});

test("audited UNKNOWN recovery unlocks a fresh review but never writes automatically", async ({
  page,
}, testInfo) => {
  const { approvals, previews, errors } = await openOrders(page);
  const work = {
    id: "unknown-work",
    scope,
    state: "UNKNOWN",
    automatic: false,
    command: {
      id: "unknown-command",
      action: "mirror_update",
      posting_number: "SYNTHETIC-1",
      correlation: { customer_order_id: "native-synthetic" },
    },
    receipt: { state: "UNKNOWN", code: "TRANSPORT_ERROR" },
  };
  let checked = false;
  let resolved = false;
  let resolves = 0;
  const evidence = {
    id: "evidence-1",
    scope,
    work_id: work.id,
    created_at: new Date().toISOString(),
    expires_at: new Date(Date.now() + 60000).toISOString(),
    source: {
      status: known("awaiting_packaging"),
      observed_at: new Date().toISOString(),
    },
    target: {
      complete: true,
      positions: [],
      observed_at: new Date().toISOString(),
    },
  };
  const view = () => ({
    work,
    eligible: checked && !resolved,
    blockers: checked ? [] : [{ code: "RECOVERY_CHECK_REQUIRED" }],
    ...(checked ? { evidence } : {}),
    ...(resolved
      ? {
          resolution: {
            id: "resolution-1",
            accepted_uncertainty: true,
            created_at: new Date().toISOString(),
            attestation: {
              id: "attestation-1",
              valid_until: new Date(Date.now() + 60000).toISOString(),
            },
          },
        }
      : {}),
  });
  await page.route("**/api/orders/operations?*", (route) =>
    fulfill(route, {
      operations: [
        {
          id: "old-op",
          scope,
          created_at: new Date().toISOString(),
          state: "UNKNOWN",
          targets: [work],
        },
      ],
    }),
  );
  await page.route("**/api/orders/recovery?*", (route) =>
    fulfill(route, view()),
  );
  await page.route("**/api/orders/recovery/check", (route) => {
    checked = true;
    return fulfill(route, view());
  });
  await page.route("**/api/orders/recovery/resolve", (route) => {
    const body = route.request().postDataJSON();
    expect(body.work_id).toBe(work.id);
    expect(body.acknowledge).toBe(true);
    expect(body.worker_stopped).toBe(true);
    expect(body.evidence_id).toBe(evidence.id);
    resolved = true;
    resolves++;
    return fulfill(route, view());
  });
  await showHistory(page);
  await page
    .getByRole("button", { name: "Обновить статусы", exact: true })
    .click();
  await openHistoryOperation(page);
  await page
    .getByText("Проверить неизвестный результат и варианты восстановления", {
      exact: true,
    })
    .click();
  await expect(
    page.getByText("Запросите свежую сверку источника и заказа МойСклад."),
  ).toBeVisible();
  expect(resolves).toBe(0);
  await page.getByRole("button", { name: "Сверить результат чтением" }).click();
  const resolve = page.getByRole("button", {
    name: "Записать разрешение нового решения",
  });
  await expect(resolve).toBeDisabled();
  await page
    .getByLabel("Основание восстановления (не менее 16 символов)")
    .fill("Осознанно принимаю риск позднего запроса после сверки");
  await page
    .getByLabel(
      "Прежний обработчик остановлен; активная отправка этого заказа исключена.",
    )
    .check();
  await expect(resolve).toBeDisabled();
  await page
    .getByLabel(
      "Принимаю риск позднего применения прежнего запроса. Разрешаю только новый предпросмотр и отдельное подтверждение.",
    )
    .check();
  await page.screenshot({
    path: testInfo.outputPath("recovery-desktop.png"),
    fullPage: true,
    mask: [page.getByRole("textbox", { name: /Основание восстановления/ })],
  });
  await page.setViewportSize({ width: 390, height: 844 });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
  await page.screenshot({
    path: testInfo.outputPath("recovery-mobile.png"),
    fullPage: true,
    mask: [page.getByRole("textbox", { name: /Основание восстановления/ })],
  });
  await resolve.click();
  await expect(
    page.getByText(/Принятие неопределённости записано/),
  ).toBeVisible();
  expect(resolves).toBe(1);
  expect(approvals).toHaveLength(0);
  expect(previews).toHaveLength(0);
  await page.keyboard.press("Escape");
  await page.getByRole("tab", { name: "Отправления", exact: true }).click();
  await page
    .getByRole("checkbox", { name: "Выбрать отправление SYNTHETIC-1" })
    .check();
  await expect(
    page.getByRole("button", {
      name: "Проверить выбранные отправления",
      exact: true,
    }),
  ).toBeEnabled();
  expect(errors).toEqual([]);
});

test("native draft follows normal preview and separate merchant approval", async ({
  page,
}, testInfo) => {
  const { approvals, errors } = await openOrders(page, false, true);
  const now = new Date();
  await page.route("**/api/orders/config?*", (route) =>
    fulfill(route, {
      ...config,
      native_documents: {
        enabled: true,
        allow_posted: false,
        actions: ["demand_create", "salesreturn_create"],
        maximum_quantity: "10",
        event_max_age_seconds: 3600,
        attested_at: now.toISOString(),
        valid_until: new Date(now.getTime() + 3600000).toISOString(),
      },
    }),
  );
  const nativeCommand: OrderCommand = {
    id: "native-command",
    action: "demand_create",
    posting_number: "SYNTHETIC-1",
    correlation: { customer_order_id: "native-parent" },
    native_movement: {
      applicable: { presence: "zero", value: false },
      organization_id: "org",
      agent_id: "agent",
      store_id: "store",
      currency_id: "currency",
      moment: now.toISOString(),
      lines: [
        {
          identity: { offer_id: "SYNTHETIC-SKU" },
          order_position_id: "position-1",
          quantity: "1",
          price: { amount: "0", currency: "RUB" },
          discount: known("0"),
          vat: known("0"),
          vat_enabled: known(false),
        },
      ],
    },
  };
  let proposed = false;
  await page.route("**/api/orders/preview", (route) => {
    const body = route.request().postDataJSON();
    expect(body.proposals).toBeUndefined();
    expect(body.native_proposals[0]).toMatchObject({
      action: "demand_create",
      applicable: { presence: "zero", value: false },
      event_kind: "draft_plan",
      event_confirmed: false,
    });
    expect(body.native_proposals[0].lines[0].quantity).toBe("1");
    nativeCommand.native_movement!.moment =
      body.native_proposals[0].planned_moment;
    proposed = true;
    return fulfill(route, {
      id: "preview-synthetic",
      scope,
      config_revision: 1,
      expires_at: new Date(Date.now() + 60000).toISOString(),
      decisions: [
        { ...decision("SYNTHETIC-1", true), commands: [nativeCommand] },
      ],
    });
  });
  await page
    .getByText("Отгрузка и возврат в МойСклад", { exact: true })
    .click();
  await page
    .getByRole("textbox", { name: /^Количество · Тестовый товар/ })
    .fill("1");
  await page
    .getByLabel("Плановое время документа")
    .fill(new Date(now.getTime() + 3600000).toISOString().slice(0, 16));
  await page
    .getByLabel("Идентификатор планирования")
    .fill("PRIVATE-SYNTHETIC-PLAN");
  expect(approvals).toHaveLength(0);
  await page
    .getByRole("button", { name: "Проверить документ перед созданием" })
    .click();
  await expect.poll(() => proposed).toBe(true);
  await expect(
    page.getByText("Проверка складского документа", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText(/Будет создан непроведённый документ/),
  ).toBeVisible();
  expect(approvals).toHaveLength(0);
  await page.screenshot({
    path: testInfo.outputPath("native-draft-desktop.png"),
    fullPage: true,
    mask: [page.getByLabel("Идентификатор планирования")],
  });
  await page.setViewportSize({ width: 390, height: 844 });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
  await page.screenshot({
    path: testInfo.outputPath("native-draft-mobile.png"),
    fullPage: true,
    mask: [page.getByLabel("Идентификатор планирования")],
  });
  await page.getByLabel("Подтвердить отправление SYNTHETIC-1").check();
  await page
    .getByLabel(
      "Я проверил фактическое основание, состав и проведение документа и подтверждаю создание складского документа в МойСклад.",
    )
    .check();
  await page
    .getByRole("button", { name: "Создать складские документы (1)" })
    .click();
  await expect.poll(() => approvals.length).toBe(1);
  expect(errors).toEqual([]);
});

test("country command explains requirement-only reconciliation and never invents a default", async ({
  page,
}, testInfo) => {
  const { approvals, previews, errors } = await openOrders(page, false, true);
  await page.route("**/api/orders/action-options?*", (route) =>
    fulfill(route, {
      scope,
      posting_number: "SYNTHETIC-1",
      source_revision: "synthetic-v1",
      actions: [
        {
          action: "posting_country_set",
          allowed: true,
          blockers: [],
          parameters_required: [
            "country_sku",
            "country_iso_code",
            "country_confirmed",
          ],
        },
      ],
      cancellation_reasons: [],
      label_types: [],
    }),
  );
  await page.route("**/api/orders/countries?*", (route) =>
    fulfill(route, {
      scope,
      posting_number: "SYNTHETIC-1",
      source_revision: "synthetic-v1",
      required_skus: ["123456"],
      countries: [{ name: "Страна из справочника", country_iso_code: "BY" }],
    }),
  );
  await page
    .getByText("Действия с отправлением в Ozon", { exact: true })
    .click();
  await page
    .getByLabel("Действие для отправления SYNTHETIC-1")
    .selectOption("posting_country_set");
  const preview = page.getByRole("button", {
    name: "Проверить действие в Ozon",
  });
  await expect(preview).toBeDisabled();
  await page
    .getByLabel("Товар, для которого Ozon требует страну")
    .selectOption("123456");
  await page.getByLabel("Страна из справочника Ozon").selectOption("BY");
  await expect(preview).toBeDisabled();
  await page
    .getByLabel(
      "Подтверждаю фактическую страну происхождения выбранного товара по имеющимся сведениям.",
    )
    .check();
  await expect(preview).toBeEnabled();
  await expect(
    page.getByText(/это не независимое подтверждение страны/),
  ).toBeVisible();
  await page.screenshot({
    path: testInfo.outputPath("country-desktop.png"),
    fullPage: true,
  });
  await page.setViewportSize({ width: 390, height: 844 });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
  await page.screenshot({
    path: testInfo.outputPath("country-mobile.png"),
    fullPage: true,
  });
  await preview.click();
  await expect.poll(() => previews.length).toBe(1);
  expect(previews[0].proposals?.[0].parameters).toEqual({
    country_sku: "123456",
    country_iso_code: "BY",
    country_confirmed: true,
  });
  expect(approvals).toHaveLength(0);
  await expect(
    page.getByText(
      /Ozon не предоставляет отдельное чтение установленной страны/,
    ),
  ).toBeVisible();
  expect(errors).toEqual([]);
});

test("current orders integrate with the redesigned shell on desktop and mobile", async ({
  page,
}, testInfo) => {
  await page.setViewportSize({ width: 1440, height: 1000 });
  const { errors } = await openOrders(page, false, false, true);
  await expect(
    page.getByRole("link", { name: "Заказы", exact: true }),
  ).toHaveAttribute("aria-current", "page");
  await expect(page.getByLabel("Аккаунты заказов")).toContainText("Магазин");
  await expect(page.getByLabel("Аккаунты заказов")).toContainText(
    "Учёт товаров",
  );
  await page.screenshot({
    path: testInfo.outputPath("orders-shell-desktop.png"),
    fullPage: true,
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByRole("button", { name: "Открыть меню" }).click();
  const menu = page.getByRole("dialog", { name: "Разделы STYK" });
  await expect(
    menu.getByRole("link", { name: "Заказы", exact: true }),
  ).toHaveAttribute("aria-current", "page");
  await page.keyboard.press("Escape");
  await expect(menu).toHaveCount(0);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  await page.screenshot({
    path: testInfo.outputPath("orders-shell-mobile.png"),
    fullPage: true,
  });
  expect(errors).toEqual([]);
});

async function openHistoryOperation(page: Page) {
  await page
    .getByRole("table", { name: "История действий с заказами" })
    .getByRole("button")
    .first()
    .click();
}

async function showHistory(page: Page) {
  const historyDrawer = page.locator("dialog.history-drawer[open]");
  if (await historyDrawer.count())
    await historyDrawer
      .getByRole("button", { name: "Закрыть", exact: true })
      .click();
  const preview = page.getByRole("dialog", {
    name: "Предпросмотр изменений",
    exact: true,
  });
  if (await preview.count())
    await preview.getByRole("button", { name: "Закрыть", exact: true }).click();
  await page.getByRole("tab", { name: "История передач", exact: true }).click();
}

test("table and drawer visual acceptance, focus, filters and responsive layouts", async ({
  page,
}) => {
  const diagnostics: string[] = [];
  page.on("console", (message) => {
    if (message.type() === "error") diagnostics.push(message.text());
  });
  page.on("response", (response) => {
    if (response.status() >= 400)
      diagnostics.push(
        `${response.status()} ${new URL(response.url()).pathname}`,
      );
  });
  await page.setViewportSize({ width: 1600, height: 1000 });
  const { approvals, previews, errors } = await openOrders(
    page,
    false,
    false,
    true,
  );
  const now = new Date().toISOString();
  const statuses = [
    "awaiting_packaging",
    "awaiting_deliver",
    "delivering",
    "cancelled",
    "delivered",
    "awaiting_deliver",
    "delivering",
  ];
  const rows = statuses.map((status, index) => ({
    ...posting(`SYNTHETIC-${index + 1}`),
    order_number: known(`TEST-ORDER-${index + 1}`),
    status: known(status),
    financial: {
      order_total: known({
        amount: String(1290 + index * 1500),
        currency: "RUB",
      }),
    },
    warehouse_id: known(index % 2 ? "Склад Ozon для теста" : "Склад Москва"),
    evidence: { ...posting("x").evidence, observed_at: now, checked_at: now },
    lines: [
      {
        identity: { sku: "581204", offer_id: "TEST-MOUSE" },
        quantity: "1",
        name: known("Беспроводная мышь"),
        price: known({ amount: "1290", currency: "RUB" }),
        cancelled_quantity: known("0"),
      },
      {
        identity: { sku: "892102", offer_id: "TEST-KEYBOARD" },
        quantity: "2",
        name: known("Клавиатура компактная"),
        price: known({ amount: "750", currency: "RUB" }),
        cancelled_quantity: known("0"),
      },
    ],
  }));
  const requestFilters: URLSearchParams[] = [];
  await page.route("**/api/orders/postings?*", (route) => {
    const params = new URL(route.request().url()).searchParams;
    requestFilters.push(params);
    return fulfill(route, {
      postings: rows,
      target_states: rows.map((row, index) => ({
        scope,
        posting_number: row.posting_number,
        has_operation: index !== 3,
        state: index === 1 || index === 5 ? "BLOCKED" : "RECONCILED",
        unresolved: false,
        source_changed: index === 1 || index === 5,
        checked_at: now,
      })),
    });
  });
  await page
    .getByRole("button", { name: "Обновить список отправлений" })
    .click();
  await expect(
    page.getByRole("table", { name: "Отправления Ozon" }).locator("tbody tr"),
  ).toHaveCount(7);
  const capture = async (name: string) => {
    await page.screenshot({
      path: new URL(`../../.local/orders-redesign/${name}.png`, import.meta.url)
        .pathname,
      fullPage: false,
      animations: "disabled",
    });
  };
  await capture("01-table-desktop");
  await page
    .getByLabel("Выбрать отправление SYNTHETIC-1", { exact: true })
    .check();
  await page
    .getByLabel("Выбрать отправление SYNTHETIC-2", { exact: true })
    .check();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await capture("02-selected-desktop");
  const opener = page.getByRole("button", {
    name: "Открыть отправление SYNTHETIC-2",
    exact: true,
  });
  await opener.focus();
  await opener.press("Enter");
  const drawer = page.getByRole("dialog", { name: "SYNTHETIC-2" });
  await expect(drawer.getByRole("tab", { name: "Обзор" })).toBeFocused();
  await capture("03-drawer-desktop");
  await drawer.getByRole("tab", { name: "Действия" }).click();
  await capture("04-actions-desktop");
  await page.keyboard.press("Escape");
  await expect(opener).toBeFocused();
  await page
    .getByRole("button", { name: "Проверить выбранные отправления" })
    .click();
  await expect(
    page.getByRole("heading", { name: "Проверка перед передачей" }),
  ).toBeVisible();
  await capture("05-preview-desktop");
  await showHistory(page);
  await capture("06-history-desktop");
  await page.getByRole("tab", { name: "Отправления", exact: true }).click();
  for (const [width, height] of [
    [1440, 900],
    [1280, 800],
    [768, 1024],
    [390, 844],
  ]) {
    await page.setViewportSize({ width, height });
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth),
    ).toBeLessThanOrEqual(width);
    await capture(`table-${width}`);
    await page
      .getByRole("button", {
        name: "Открыть отправление SYNTHETIC-1",
        exact: true,
      })
      .click();
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth),
    ).toBeLessThanOrEqual(width);
    await expect(
      page
        .getByRole("dialog")
        .getByRole("button", { name: "Закрыть", exact: true }),
    ).toBeInViewport();
    await capture(`drawer-${width}`);
    await page.keyboard.press("Escape");
  }
  await page
    .getByRole("combobox", { name: "Статус Ozon", exact: true })
    .selectOption("delivered");
  await expect
    .poll(() => requestFilters.at(-1)?.get("status"))
    .toBe("delivered");
  await page
    .getByRole("combobox", { name: "Состояние отправлений", exact: true })
    .selectOption("attention");
  await expect.poll(() => requestFilters.at(-1)?.get("attention")).toBe("true");
  expect(previews).toHaveLength(1);
  expect(approvals).toHaveLength(0);
  expect(errors).toEqual([]);
  expect(diagnostics).toEqual([]);
});

test("hover keeps labels readable across orders tabs, table, drawer, settings and preview", async ({
  page,
}, testInfo) => {
  test.setTimeout(90_000);
  await page.setViewportSize({ width: 1440, height: 1000 });
  const { errors, approvals } = await openOrders(page, false, false, true);
  const results: Record<string, unknown> = {};
  results.table = await auditButtonHovers(page, "table and shared navigation");
  await page
    .getByLabel("Выбрать отправление SYNTHETIC-1", { exact: true })
    .check();
  results.selection = await auditButtonHovers(page, "bulk selection");
  await page
    .getByRole("button", {
      name: "Открыть отправление SYNTHETIC-1",
      exact: true,
    })
    .click();
  results.overview = await auditButtonHovers(page, "drawer overview");
  for (const name of ["Товары", "Действия", "История"]) {
    await page
      .getByRole("dialog")
      .getByRole("tab", { name, exact: true })
      .click();
    results[name] = await auditButtonHovers(page, `drawer ${name}`);
  }
  await page.keyboard.press("Escape");
  await page.getByRole("button", { name: "Настройки", exact: true }).click();
  await expect(
    page.getByRole("option", { name: "Тестовая организация" }),
  ).toBeAttached();
  results.settings = await auditButtonHovers(page, "settings");
  await page.getByRole("button", { name: "Настройки", exact: true }).click();
  await page
    .getByRole("button", {
      name: "Проверить выбранные отправления",
      exact: true,
    })
    .click();
  await expect(
    page.getByRole("heading", { name: "Проверка перед передачей" }),
  ).toBeVisible();
  results.preview = await auditButtonHovers(page, "preview");
  await showHistory(page);
  results.history = await auditButtonHovers(page, "history");
  await page.getByRole("tab", { name: "Отправления", exact: true }).click();
  const selectedTab = page.getByRole("tab", {
    name: "Отправления",
    exact: true,
  });
  await selectedTab.hover();
  await page.screenshot({
    path: new URL(
      "../../.local/orders-redesign/hover-fixed.png",
      import.meta.url,
    ).pathname,
    animations: "disabled",
  });
  await testInfo.attach("hover-contrast", {
    body: JSON.stringify(results, null, 2),
    contentType: "application/json",
  });
  expect(approvals).toHaveLength(0);
  expect(errors).toEqual([]);
});

function journalOperation(
  index: number,
  state: string,
  action = "mirror_update",
) {
  const number = `00012345-${String(index + 1).padStart(4, "0")}`;
  return {
    id: `journal-operation-${index}`,
    scope,
    state,
    created_at: `2026-11-${index < 5 ? "05" : "04"}T${String(15 - (index % 8)).padStart(2, "0")}:01:00Z`,
    targets: [
      {
        id: `journal-work-${index}`,
        scope,
        state,
        automatic: index % 2 === 0,
        command: {
          id: `journal-command-${index}`,
          scope,
          action,
          posting_number: number,
          commercial_order_id: "00012345",
          correlation: {
            customer_order_id: "11111111-1111-4111-8111-111111111111",
          },
        },
        receipt: {
          state,
          code: state === "UNKNOWN" ? "TRANSPORT_ERROR" : undefined,
        },
      },
    ],
  };
}
function journalMatches(state: string, params: URLSearchParams) {
  return (
    (!params.get("status") || params.get("status") === state) &&
    (params.get("attention") !== "true" ||
      ["UNKNOWN", "REJECTED", "BLOCKED"].includes(state))
  );
}

test("history journal matches the references with safe details, focus and responsive layouts", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1600, height: 1000 });
  await page.clock.install({ time: new Date("2026-11-05T16:00:00Z") });
  const { approvals, errors } = await openOrders(page, false, false, true);
  const operations = [
    journalOperation(0, "RECONCILED", "mirror_create"),
    journalOperation(1, "ACKNOWLEDGED", "posting_pack"),
    journalOperation(2, "UNKNOWN"),
    journalOperation(3, "REJECTED", "posting_cancel"),
    journalOperation(4, "RECONCILED", "posting_label_create"),
    journalOperation(5, "RECONCILED", "demand_create"),
    journalOperation(6, "POLLING", "mirror_composition_update"),
  ];
  const reads: URLSearchParams[] = [];
  await page.route("**/api/orders/operations?*", (route) => {
    expect(route.request().method()).toBe("GET");
    const params = new URL(route.request().url()).searchParams;
    reads.push(params);
    return fulfill(route, {
      operations: operations.filter((item) =>
        journalMatches(item.state, params),
      ),
    });
  });
  const uncertain = operations[2].targets[0];
  await page.route("**/api/orders/recovery?*", (route) =>
    fulfill(route, {
      work: uncertain,
      eligible: false,
      blockers: [{ code: "RECOVERY_CHECK_REQUIRED" }],
    }),
  );
  await showHistory(page);
  await page
    .getByRole("button", { name: "Обновить статусы", exact: true })
    .click();
  const table = page.getByRole("table", {
    name: "История действий с заказами",
  });
  await expect(table.locator("tr.history-row")).toHaveCount(7);
  await expect(table.locator(".history-status.success")).toHaveCount(3);
  await expect(
    table.getByText("Ждём подтверждения", { exact: true }),
  ).toBeVisible();
  await page.evaluate(() => document.fonts.ready);
  const capture = async (name: string) =>
    page.screenshot({
      path: new URL(`../../.local/orders-history/${name}.png`, import.meta.url)
        .pathname,
      animations: "disabled",
    });
  await capture("01-history-1600");
  await page
    .getByRole("button", { name: "Нужна проверка", exact: true })
    .click();
  await expect(table.locator("tr.history-row")).toHaveCount(2);
  expect(reads.some((params) => params.get("attention") === "true")).toBe(true);
  await capture("02-attention");
  await page.getByRole("button", { name: "Все", exact: true }).click();
  await expect(table.locator("tr.history-row")).toHaveCount(7);
  const success = table.getByRole("button", {
    name: /Подробнее: 00012345-0001/,
  });
  await success.focus();
  await success.press("Enter");
  let drawer = page.getByRole("dialog");
  await expect(
    drawer.getByRole("heading", { name: "Готово", exact: true }),
  ).toBeVisible();
  await expect(
    drawer.getByRole("link", { name: "Открыть заказ МойСклад" }),
  ).toHaveAttribute("href", /customerorder\/edit\?id=11111111/);
  await expect(drawer.getByText("Work ID", { exact: true })).toHaveCount(0);
  await capture("03-success");
  await page.keyboard.press("Escape");
  await expect(success).toBeFocused();
  const unknown = table.getByRole("button", {
    name: /Разобраться: 00012345-0003/,
  });
  await unknown.focus();
  await unknown.press("Enter");
  drawer = page.getByRole("dialog");
  await expect(drawer.getByText(/Запрос мог выполниться/)).toBeVisible();
  await expect(drawer.getByText(/может создать дубликат/)).toBeVisible();
  expect(await drawer.evaluate((node) => node.matches(":modal"))).toBe(true);
  await capture("04-unknown");
  await drawer.getByRole("button", { name: "Закрыть", exact: true }).focus();
  await page.keyboard.press("Tab");
  await expect(
    drawer.getByRole("button", { name: "История передач", exact: true }),
  ).toBeFocused();
  await drawer.getByText("Технические сведения", { exact: true }).click();
  await expect(
    drawer.getByText("journal-work-2", { exact: true }),
  ).toBeVisible();
  await drawer
    .getByText("journal-work-2", { exact: true })
    .scrollIntoViewIfNeeded();
  await capture("05-technical");
  await drawer.getByText("Технические сведения", { exact: true }).click();
  await drawer
    .getByText("Проверить неизвестный результат и варианты восстановления", {
      exact: true,
    })
    .click();
  await expect(
    drawer.getByText("Запросите свежую сверку источника и заказа МойСклад."),
  ).toBeVisible();
  await drawer
    .getByText("Запросите свежую сверку источника и заказа МойСклад.")
    .scrollIntoViewIfNeeded();
  await capture("06-recovery");
  await page.keyboard.press("Escape");
  for (const [width, height] of [
    [1440, 900],
    [1280, 800],
    [768, 1024],
    [390, 844],
  ]) {
    await page.setViewportSize({ width, height });
    await page.evaluate(() => window.scrollTo(0, 0));
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth),
    ).toBeLessThanOrEqual(width);
    await capture(`history-${width}`);
    if (width === 390)
      await page.screenshot({
        path: new URL(
          "../../.local/orders-history/history-mobile-full.png",
          import.meta.url,
        ).pathname,
        fullPage: true,
        animations: "disabled",
      });
    await unknown.click();
    await expect(
      page
        .getByRole("dialog")
        .getByRole("heading", { name: "Нужна проверка", exact: true }),
    ).toBeVisible();
    expect(
      await page
        .locator(".history-drawer-frame")
        .evaluate((node) => node.scrollWidth <= node.clientWidth),
    ).toBe(true);
    await capture(`drawer-${width}`);
    await page.keyboard.press("Escape");
    await expect(unknown).toBeFocused();
  }
  expect(approvals).toHaveLength(0);
  expect(errors).toEqual([]);
});

test("history search and combined result filters reach older pages and keep mixed targets separate", async ({
  page,
}) => {
  await page.clock.install({ time: new Date("2026-11-05T16:00:00Z") });
  const { approvals, errors } = await openOrders(page);
  const historyReads: URLSearchParams[] = [];
  const operations = Array.from({ length: 32 }, (_, index) =>
    journalOperation(
      index,
      index < 25 ? "RECONCILED" : index % 2 ? "UNKNOWN" : "REJECTED",
    ),
  );
  operations[0].targets.push({
    ...operations[31].targets[0],
    id: "mixed-unknown",
    command: {
      ...operations[31].targets[0].command,
      posting_number: "MIXED-OTHER",
    },
  });
  await page.route("**/api/orders/operations?*", (route) => {
    const params = new URL(route.request().url()).searchParams;
    historyReads.push(params);
    const matched = operations
      .map((operation) => ({
        ...operation,
        targets: operation.targets.filter(
          (work) =>
            (!params.get("search") ||
              work.command.posting_number === params.get("search")) &&
            journalMatches(work.state, params),
        ),
      }))
      .filter((operation) => operation.targets.length);
    const offset = Number(params.get("cursor") || "0");
    return fulfill(route, {
      operations: matched.slice(offset, offset + 25),
      next_cursor:
        matched.length > offset + 25 ? String(offset + 25) : undefined,
    });
  });
  await showHistory(page);
  await page
    .getByRole("button", { name: "Обновить статусы", exact: true })
    .click();
  const table = page.getByRole("table", {
    name: "История действий с заказами",
  });
  await expect(table.locator("tr.history-row")).toHaveCount(26);
  await page.getByRole("button", { name: "Следующие", exact: true }).click();
  await expect(
    table.getByRole("button", { name: /Разобраться: 00012345-0032/ }),
  ).toBeVisible();
  await expect(page.getByText("Страница 2", { exact: true })).toBeVisible();
  await page
    .getByRole("button", { name: "Нужна проверка", exact: true })
    .click();
  await expect(table.locator("tr.history-row")).toHaveCount(8);
  await expect(table.locator(".history-status.success")).toHaveCount(0);
  await expect(page.getByText("Страница 1", { exact: true })).toBeVisible();
  await page.getByLabel("Номер отправления в истории").fill("00012345-0032");
  await expect(table.locator("tr.history-row")).toHaveCount(1);
  await page.getByLabel("Результат операции").selectOption("REJECTED");
  await expect(
    page.getByText("Операции не найдены", { exact: true }),
  ).toBeVisible();
  await page
    .getByRole("button", { name: "Сбросить фильтры истории", exact: true })
    .click();
  await expect(table.locator("tr.history-row")).toHaveCount(26);
  await page.getByLabel("Номер отправления в истории").fill("00012345-0001");
  await page.getByLabel("Результат операции").selectOption("UNKNOWN");
  await expect(
    page.getByText("Операции не найдены", { exact: true }),
  ).toBeVisible();
  const latestReads = () =>
    historyReads.filter(
      (params) =>
        !params.get("search") &&
        !params.get("status") &&
        params.get("attention") !== "true" &&
        !params.get("cursor"),
    ).length;
  const beforePoll = latestReads();
  await page.clock.fastForward(10001);
  await expect.poll(latestReads).toBeGreaterThan(beforePoll);
  expect(approvals).toHaveLength(0);
  expect(errors).toEqual([]);
});

test("history account switching clears filters, cursors, details and cached rows", async ({
  page,
}) => {
  await openOrders(page, false, false, true);
  await page.route("**/api/registrations?*", (route) =>
    fulfill(route, {
      accounts: [
        {
          source: "moysklad",
          account: scope.moysklad_account,
          name: "Учёт товаров",
        },
        { source: "ozon", account: scope.ozon_account, name: "Магазин" },
        { source: "ozon", account: "second-ozon", name: "Второй магазин" },
      ],
      truncated: false,
    }),
  );
  await page.route("**/api/orders/config?*", (route) => {
    const account = new URL(route.request().url()).searchParams.get(
      "ozon_account",
    )!;
    return fulfill(route, {
      ...config,
      scope: { ...scope, ozon_account: account },
    });
  });
  await page.route("**/api/orders/postings?*", (route) =>
    fulfill(route, { postings: [] }),
  );
  await page.route("**/api/orders/operations?*", (route) => {
    const account = new URL(route.request().url()).searchParams.get(
      "ozon_account",
    );
    const operation = journalOperation(0, "UNKNOWN");
    const second = account === "second-ozon";
    const currentScope = {
      ...scope,
      ozon_account: second ? "second-ozon" : scope.ozon_account,
    };
    return fulfill(route, {
      operations: [
        {
          ...operation,
          scope: currentScope,
          targets: operation.targets.map((work) => ({
            ...work,
            scope: currentScope,
            command: {
              ...work.command,
              scope: currentScope,
              posting_number: second ? "SECOND-ACCOUNT" : "FIRST-ACCOUNT",
            },
          })),
        },
      ],
      next_cursor: "next",
    });
  });
  await page.reload();
  await showHistory(page);
  const table = page.getByRole("table", {
    name: "История действий с заказами",
  });
  await expect(table.getByText("FIRST-ACCOUNT", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Следующие", exact: true }).click();
  await expect(page.getByText("Страница 2", { exact: true })).toBeVisible();
  await page
    .getByRole("button", { name: "Нужна проверка", exact: true })
    .click();
  await expect(
    table.getByRole("button", { name: /Разобраться: FIRST-ACCOUNT/ }),
  ).toBeVisible();
  await table
    .getByRole("button", { name: /Разобраться: FIRST-ACCOUNT/ })
    .click();
  await expect(page.getByRole("dialog")).toBeVisible();
  await page.keyboard.press("Escape");
  await page
    .getByRole("combobox", { name: "Ozon", exact: true })
    .selectOption("second-ozon");
  await showHistory(page);
  await expect(
    table.getByText("SECOND-ACCOUNT", { exact: true }),
  ).toBeVisible();
  await expect(table.getByText("FIRST-ACCOUNT", { exact: true })).toHaveCount(
    0,
  );
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(page.getByLabel("Номер отправления в истории")).toHaveValue("");
  await expect(
    page.getByRole("button", { name: "Все", exact: true }),
  ).toHaveAttribute("aria-pressed", "true");
  await expect(
    page
      .getByRole("navigation", { name: "Страницы истории" })
      .getByText("Страница 1", { exact: true }),
  ).toBeVisible();
});
