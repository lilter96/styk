import { expect, test, type Page, type Route } from "@playwright/test";
import type {
  CatalogConfig,
  CatalogProduct,
  Decision,
} from "../src/CatalogSyncTypes";

// Component browser contract tests. Root owns the real Go/Postgres browser harness.
// A tiny Vite entry keeps these mocks independent of App.tsx route ownership.
const environment = globalThis as typeof globalThis & {
  process: { env: Record<string, string | undefined> };
};
const origin =
  environment.process.env.STYK_CATALOG_UI_ORIGIN || "http://127.0.0.1:5173";
const scope = {
  tenant: "synthetic-merchant",
  moysklad_account: "synthetic-moy",
  ozon_account: "synthetic-ozon",
};
const account = (provider: "moysklad" | "ozon") => ({
  provider,
  id: provider === "moysklad" ? scope.moysklad_account : scope.ozon_account,
});
const identity = (id: string, provider: "moysklad" | "ozon") => ({
  id,
  account: account(provider),
});
const product = (id: string, name: string): CatalogProduct => ({
  identity: identity(id, "moysklad"),
  revision: "synthetic-v1",
  lifecycle: "active",
  fields: { name: { state: "known", data: name } },
});
const first = product("source-1", "Чашка фарфоровая");
const second = product("source-2", "Тарелка керамическая");
const enrollments = [first, second].map((source, index) => ({
  id: `enrollment-${index + 1}`,
  mapping_id: `mapping-${index + 1}`,
  mapping_version: 1,
  source: source.identity,
  target: identity(`target-${index + 1}`, "ozon"),
  automatic: false,
}));
const config = {
  scope,
  revision: 1,
  enabled: true,
  test_enabled: true,
  publication_available: true,
  interval_seconds: 300,
  rules: {
    name: "MOYSKLAD_AUTHORITATIVE",
    description: "OZON_AUTHORITATIVE",
    category: "IGNORE",
    attributes: "IGNORE",
  },
  targets: enrollments,
  updated_at: "2026-10-29T12:00:00Z",
};
function decision(index: number, conflict = false) {
  const source = [first, second][index];
  const target = { ...source, identity: enrollments[index].target };
  const change = {
    field: "name",
    source: source.identity,
    target: target.identity,
    before: { state: "known", data: "Старое название" },
    after: source.fields.name,
  };
  return {
    enrollment_id: enrollments[index].id,
    source,
    target,
    target_absent: false,
    ready: !conflict,
    validation: [],
    commands: conflict
      ? []
      : [
          {
            action: "update",
            target: target.identity,
            values: { name: change.after },
          },
        ],
    plan: {
      mapping_id: enrollments[index].mapping_id,
      mapping_version: 1,
      fingerprint: `synthetic-fingerprint-${index}`,
      changes: conflict ? [] : [change],
      conflicts: conflict
        ? [
            {
              field: "name",
              reason: "both_sides_changed",
              source_value: source.fields.name,
              target_value: { state: "known", data: "Изменено в Ozon" },
            },
          ]
        : [],
      skipped: [],
      noop: false,
    },
  };
}
function operation(state: string) {
  const d = decision(0);
  return {
    id: "operation-synthetic",
    scope,
    preview_id: "preview-synthetic",
    state,
    created_at: "2026-10-29T12:00:00Z",
    targets: [
      {
        id: "work-synthetic",
        enrollment_id: enrollments[0].id,
        state,
        command: d.commands[0],
        decision: d,
        receipt: {
          state: state === "PROCESSING" ? "accepted" : "unknown",
          task_id: state === "PROCESSING" ? "task-synthetic" : undefined,
        },
      },
    ],
  };
}
const fulfill = (route: Route, body: unknown, status = 200) =>
  route.fulfill({
    status,
    contentType: "application/json",
    body: JSON.stringify(body),
  });

async function openCatalog(
  page: Page,
  options: { lostApproval?: boolean; unmapped?: boolean } = {},
) {
  const approvals: unknown[] = [];
  const browserErrors: string[] = [];
  page.on("pageerror", (error) => browserErrors.push(error.message));
  // Use Vite's exact dependency URL so context is shared with the component.
  const source = await (
    await page.request.get(`${origin}/src/CatalogSync.tsx`)
  ).text();
  const queryModule = source.match(
    /from "([^"\n]*@tanstack_react-query\.js[^"\n]*)"/,
  )?.[1];
  if (!queryModule)
    throw new Error(
      "Start the Vite development server before catalog UI browser tests",
    );
  let submitted = false;
  let currentConfig = structuredClone(config) as CatalogConfig;
  const unmapped = {
    ...product("unmapped-ozon", "Новый товар Ozon"),
    identity: {
      ...identity("unmapped-ozon", "ozon"),
      offer_id: "SYNTHETIC-UNMAPPED",
    },
  };
  const creationDecisions: Decision[] = [];
  await page.route("**/*", (route) => {
    const host = new URL(route.request().url()).hostname;
    return ["127.0.0.1", "localhost"].includes(host)
      ? route.continue()
      : route.abort();
  });
  await page.route("**/api/**", async (route) => {
    const request = route.request(),
      url = new URL(request.url());
    if (url.pathname.endsWith("/config")) {
      if (request.method() === "POST")
        currentConfig = {
          ...request.postDataJSON().config,
          revision: currentConfig.revision + 1,
        };
      return fulfill(route, currentConfig);
    }
    if (url.pathname.endsWith("/products"))
      return fulfill(
        route,
        options.unmapped && url.searchParams.get("provider") === "ozon"
          ? { products: [unmapped], enrollments: [] }
          : url.searchParams.get("cursor") === "page-2"
            ? { products: [second], enrollments: [enrollments[1]] }
            : {
                products: [first],
                enrollments: [enrollments[0]],
                next_cursor: "page-2",
              },
      );
    if (url.pathname.endsWith("/preview")) {
      const body = request.postDataJSON();
      expect(body.tenant).toBe(scope.tenant);
      expect(body.expected_revision).toBe(currentConfig.revision);
      const decisions = body.enrollment_ids.map((id: string) => {
        const enrolled = currentConfig.targets.find(
          (target) => target.id === id,
        );
        if (!enrolled?.create)
          return decision(
            id === enrollments[0].id ? 0 : 1,
            id === enrollments[1].id,
          );
        const value: Decision = {
          ...decision(0),
          enrollment_id: id,
          source: unmapped,
          target: { identity: enrolled.target, fields: {}, revision: "" },
          target_absent: true,
          commands: [
            {
              action: "create",
              target: enrolled.target,
              values: enrolled.create.values,
            },
          ],
          plan: {
            mapping_id: "",
            mapping_version: 0,
            fingerprint: "explicit-creation",
            changes: [],
            conflicts: [],
            skipped: [],
            noop: false,
          },
        };
        creationDecisions.push(value);
        return value;
      });
      return fulfill(route, {
        id: "preview-synthetic",
        scope,
        config_revision: currentConfig.revision,
        expires_at: new Date(Date.now() + 60000).toISOString(),
        decisions,
      });
    }
    if (url.pathname.endsWith("/approve")) {
      approvals.push(request.postDataJSON());
      submitted = true;
      if (options.lostApproval) return route.abort("failed");
      const accepted = operation("PROCESSING");
      return fulfill(
        route,
        options.unmapped
          ? {
              ...accepted,
              targets: accepted.targets.map((work) => ({
                ...work,
                enrollment_id: creationDecisions[0].enrollment_id,
                decision: creationDecisions[0],
                command: creationDecisions[0].commands[0],
              })),
            }
          : accepted,
      );
    }
    if (url.pathname.endsWith("/operations"))
      return fulfill(route, {
        operations: submitted
          ? [operation(options.lostApproval ? "UNKNOWN" : "PROCESSING")]
          : [],
      });
    if (url.pathname.endsWith("/categories"))
      return fulfill(route, { items: [] });
    throw new Error(`Unexpected synthetic API path: ${url.pathname}`);
  });
  await page.route("**/__catalog-sync-ui", (route) =>
    route.fulfill({
      contentType: "text/html",
      body: `<!doctype html><html lang="ru"><head><meta charset="UTF-8" /><meta name="viewport" content="width=device-width, initial-scale=1" /><title>STYK: каталог</title></head><body class="reference-shell"><main style="max-width:1100px;margin:24px auto;padding:16px"><div id="catalog-root"></div></main>
    <script type="module">import RefreshRuntime from '/@react-refresh';RefreshRuntime.injectIntoGlobalHook(window);window.$RefreshReg$=()=>{};window.$RefreshSig$=()=>type=>type;window.__vite_plugin_react_preamble_installed__=true;</script>
    <script type="module">import React from '/node_modules/.vite/deps/react.js';import ReactDOM from '/node_modules/.vite/deps/react-dom_client.js';import {QueryClient,QueryClientProvider} from '${queryModule}';import {CatalogSync} from '/src/CatalogSync.tsx';import '/src/style.css';import '/src/imports.css';import '/src/mapping.css';ReactDOM.createRoot(document.getElementById('catalog-root')).render(React.createElement(QueryClientProvider,{client:new QueryClient({defaultOptions:{queries:{retry:false}}})},React.createElement(CatalogSync,{scope:{tenant:'synthetic-merchant',moy:'synthetic-moy',ozon:'synthetic-ozon'},moyLabel:'Учёт товаров',ozonLabel:'Магазин'})));</script>
    </body></html>`,
    }),
  );
  await page.goto(`${origin}/__catalog-sync-ui`);
  await expect
    .poll(async () => ({
      errors: browserErrors,
      headings: await page
        .getByRole("heading", { name: "Синхронизация каталога", exact: true })
        .count(),
    }))
    .toEqual({ errors: [], headings: 1 });
  await expect(
    page.getByRole("heading", { name: "Синхронизация каталога", exact: true }),
  ).toBeVisible();
  return approvals;
}

test("reviews paginated products on desktop/mobile and approves only an explicit conflict-free selection", async ({
  page,
}, testInfo) => {
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  await page.setViewportSize({ width: 1440, height: 1000 });
  const approvals = await openCatalog(page);
  const firstChoice = page.getByLabel("Выбрать товар Чашка фарфоровая");
  await firstChoice.focus();
  await firstChoice.press("Space");
  await page.getByRole("button", { name: "Следующие товары" }).click();
  await page.getByLabel("Выбрать товар Тарелка керамическая").check();
  await expect(
    page.getByText("Выбрано товаров: 2", { exact: true }),
  ).toBeVisible();
  await page
    .getByRole("button", { name: "Проверить выбранные товары" })
    .click();
  await expect(
    page.getByText("Название: конфликт", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByLabel("Подтвердить изменения товара Тарелка керамическая"),
  ).toBeDisabled();
  await expect(
    page.getByRole("button", { name: "Отправить изменения (0)" }),
  ).toBeDisabled();
  expect(approvals).toHaveLength(0);
  await page.screenshot({
    path: testInfo.outputPath("catalog-sync-desktop.png"),
    fullPage: true,
  });
  await page.setViewportSize({ width: 390, height: 844 });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
  await page.screenshot({
    path: testInfo.outputPath("catalog-sync-mobile.png"),
    fullPage: true,
  });
  const choice = page.getByLabel(
    "Подтвердить изменения товара Чашка фарфоровая",
  );
  await choice.focus();
  await choice.press("Space");
  const submit = page.getByRole("button", { name: "Отправить изменения (1)" });
  await expect(submit).toBeDisabled();
  await page.getByLabel(/Я проверил изменения выбранных товаров/).check();
  await submit.click();
  await expect(page.getByText(/Подтверждение записано/)).toBeVisible();
  expect(approvals).toEqual([
    {
      ...scope,
      preview_id: "preview-synthetic",
      enrollment_ids: [enrollments[0].id],
      acknowledge: true,
      idempotency_key: expect.any(String),
    },
  ]);
  await expect(
    page.getByText(/Принятие запроса ещё не подтверждает/),
  ).toBeVisible();
  await expect(
    page.getByText("Изменения подтверждены сверкой", { exact: true }),
  ).toHaveCount(0);
  expect(await page.locator("body").innerText()).not.toMatch(
    /workspace|рабочее пространство/i,
  );
  expect(errors).toEqual([]);
});

test("a lost approval response exposes read-only status refresh and never retries the write", async ({
  page,
}) => {
  const approvals = await openCatalog(page, { lostApproval: true });
  await page.getByLabel("Выбрать товар Чашка фарфоровая").check();
  await page
    .getByRole("button", { name: "Проверить выбранные товары" })
    .click();
  await page
    .getByLabel("Подтвердить изменения товара Чашка фарфоровая")
    .check();
  await page.getByLabel(/Я проверил изменения выбранных товаров/).check();
  await page.getByRole("button", { name: "Отправить изменения (1)" }).click();
  await expect(
    page.getByText(/Результат подтверждения неизвестен:/),
  ).toBeVisible();
  await expect(page.getByText(/Запрос мог выполниться/)).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Отправить изменения (1)" }),
  ).toBeDisabled();
  await page.getByRole("button", { name: "Обновить статусы" }).click();
  await expect(
    page.getByRole("button", { name: "Обновить статусы" }),
  ).toBeEnabled();
  expect(approvals).toHaveLength(1);
  await expect(
    page.getByRole("button", { name: /Повторить отправку|Отправить заново/ }),
  ).toHaveCount(0);
});

test("an unmapped Ozon product gets an explicit stable creation request before separate approval", async ({
  page,
}) => {
  const saves: CatalogConfig[] = [];
  page.on("request", (request) => {
    if (
      request.method() === "POST" &&
      new URL(request.url()).pathname.endsWith("/catalog-sync/config")
    )
      saves.push(request.postDataJSON().config);
  });
  const approvals = await openCatalog(page, { unmapped: true });
  await page
    .getByRole("combobox", { name: "Список товаров", exact: true })
    .selectOption("ozon");
  await page
    .getByRole("button", { name: "Создать в МойСклад", exact: true })
    .click();
  await expect(
    page.getByLabel("Название нового товара", { exact: true }),
  ).toHaveValue("Новый товар Ozon");
  expect(saves).toHaveLength(0);
  expect(approvals).toHaveLength(0);
  await page
    .getByRole("button", { name: "Проверить выбранные товары" })
    .click();
  await expect(
    page.getByText("Будет создан новый товар", { exact: true }),
  ).toBeVisible();
  expect(saves).toHaveLength(1);
  const creation = saves[0].targets.find((target) => target.create)!;
  expect(creation.id).toMatch(/^create-[\da-f-]{36}$/);
  expect(creation.create?.external_code).toBe(
    `STYK-CATALOG-${creation.create?.id}`,
  );
  expect(creation.create?.values).toEqual({
    name: { state: "known", data: "Новый товар Ozon" },
  });
  expect(creation.target.account).toEqual(account("moysklad"));
  expect(creation.target.id).toBe("");
  expect(creation.mapping_id).toBeUndefined();
  expect(creation.automatic).toBe(false);
  expect(approvals).toHaveLength(0);
  await page
    .getByLabel("Подтвердить изменения товара Новый товар Ozon")
    .check();
  await expect(
    page.getByRole("button", { name: "Отправить изменения (1)" }),
  ).toBeDisabled();
  await page.getByLabel(/Я проверил изменения выбранных товаров/).check();
  await page.getByRole("button", { name: "Отправить изменения (1)" }).click();
  await expect(page.getByText(/Подтверждение записано/)).toBeVisible();
  expect(approvals).toHaveLength(1);
  expect(approvals[0]).toMatchObject({
    preview_id: "preview-synthetic",
    enrollment_ids: [creation.id],
    acknowledge: true,
  });
});
