import { expect, test, type Page } from "@playwright/test";
import { spawn, type ChildProcess } from "node:child_process";
import type { ImportOperation, ImportProvider, Report } from "../src/api";

// Render the real application with bounded synthetic API fixtures. Marketplace
// traffic is impossible: every API request is handled here, with unknown paths
// rejected. The separate import-management regression exercises the real API.
const origin = "http://127.0.0.1:15176";
const tenant = "synthetic-workspace-6b5b87fb-e34a-4d7e-b156-9f429db61990";
const account = "synthetic-account-68d68ff0-dac5-4c74-86c2-a997ef0e8d6a";
const observed = "2026-10-29T06:35:00Z";
let vite: ChildProcess;

test.beforeAll(async () => {
  vite = spawn(
    process.execPath,
    [
      "node_modules/vite/bin/vite.js",
      "--host",
      "127.0.0.1",
      "--port",
      "15176",
      "--strictPort",
    ],
    { stdio: "ignore" },
  );
  await expect
    .poll(
      async () => {
        try {
          return (await fetch(origin)).ok;
        } catch {
          return false;
        }
      },
      { timeout: 15000 },
    )
    .toBe(true);
});
test.afterAll(() => vite?.kill());

function operation(
  provider: ImportProvider,
  state: ImportOperation["state"] = "SUCCEEDED",
): ImportOperation {
  return {
    id: provider === "moysklad" ? "a".repeat(32) : "b".repeat(32),
    tenant,
    provider,
    account,
    operation_type:
      provider === "moysklad"
        ? "MOYSKLAD_CATALOG_IMPORT"
        : "OZON_CATALOG_IMPORT",
    state,
    phase: state === "RUNNING" ? "READING_PRODUCTS" : "COMPLETED",
    outcome:
      state === "FAILED"
        ? "API_FAILURE"
        : state === "SUCCEEDED"
          ? "COMPLETE_TRAVERSAL"
          : null,
    limiting_resource: null,
    created_at: "2026-10-29T06:34:48Z",
    started_at: "2026-10-29T06:34:49Z",
    finished_at:
      state === "RUNNING" || state === "OUTCOME_UNKNOWN" ? null : observed,
    attempt_started_at: "2026-10-29T06:34:49Z",
    progress_at: observed,
    lease_expires_at: null,
    attempt: 1,
    max_attempts: 3,
    elapsed_ms: 11000,
    batch_id:
      state === "SUCCEEDED" ? (provider === "moysklad" ? 17 : 18) : null,
    batch_created: state === "SUCCEEDED" ? true : null,
    counts: {
      requests: 5,
      attempts: 5,
      pages: 5,
      completed_requests: 5,
      completed_pages: 5,
      products: 842,
      variants: 26,
      warehouses: 3,
      observations: 842,
      warehouse_observations: 1273,
    },
    limits: {
      requests: 10000,
      attempts: 30000,
      pages: 10000,
      products: 100000,
      variants: 100000,
      warehouses: 1000,
      observations: 500000,
      warehouse_observations: 500000,
      response_bytes: 10485760,
      duration_ns: 900e9,
      database_timeout_ns: 30e9,
    },
  };
}

type Scenario =
  | "success"
  | "missing"
  | "running"
  | "failed"
  | "history-unavailable"
  | "unknown-submission"
  | "unknown-operation"
  | "key-error"
  | "sync-ready"
  | "sync-active"
  | "sync-hold";
async function openFixture(page: Page, scenario: Scenario = "success") {
  await page.clock.setFixedTime(new Date("2026-10-29T06:36:00Z"));
  await page.addInitScript(
    ({ tenant, account, missing }) => {
      sessionStorage.setItem(
        "styk-workspace",
        JSON.stringify({ tenant, moy: account, ozon: missing ? "" : account }),
      );
    },
    { tenant, account, missing: scenario === "missing" },
  );
  const operations = {
    moysklad: operation(
      "moysklad",
      scenario === "running"
        ? "RUNNING"
        : scenario === "failed"
          ? "FAILED"
          : scenario === "unknown-operation"
            ? "OUTCOME_UNKNOWN"
            : "SUCCEEDED",
    ),
    ozon: operation("ozon"),
  };
  const report: Report = {
    batches: {
      moysklad: {
        present: true,
        completed: true,
        observed_at: observed,
        age: "1m",
        counts: {
          products: 842,
          variants: 26,
          warehouses: 3,
          stock_rows: 1273,
        },
      },
      ozon: {
        present: scenario !== "missing",
        completed: scenario !== "missing",
        observed_at: observed,
        age: "1m",
        counts: { products: 798, warehouses: 2, stock_rows: 798 },
      },
    },
    checks: [],
    decision_status: "UNKNOWN",
    decision_reasons: [],
    publication: "DISABLED",
    mapping_counts: {
      products: 798,
      warehouses: 2,
      catalog_products: 798,
      catalog_confirmed_products: scenario.startsWith("sync-") ? 798 : 0,
      confirmed_warehouses: scenario.startsWith("sync-") ? 2 : 0,
    },
  };
  const calls: string[] = [];
  const submissions: string[] = [];
  const credentialBodies: string[] = [],
    syncBodies: string[] = [];
  let credential = {
    tenant,
    provider: "moysklad",
    account,
    name: "Основной магазин",
    revision: 1,
    enabled: true,
    updated_at: observed,
    checked_at: observed,
    last_request_id: "previous-save",
  };
  let sync = {
    tenant,
    moysklad_account: account,
    ozon_account: account,
    publication_available: scenario !== "sync-hold", // Synthetic fixtures only.
    enabled: scenario === "sync-active" || scenario === "sync-hold",
    state:
      scenario === "sync-active" || scenario === "sync-hold"
        ? "ACTIVE"
        : "DISABLED",
    ready: scenario.startsWith("sync-"),
    checks: [],
    targets: [],
    last_confirmed_at:
      scenario === "sync-active" || scenario === "sync-hold"
        ? observed
        : undefined,
  };

  await page.route("**/api/**", async (route) => {
    const request = route.request(),
      url = new URL(request.url());
    calls.push(request.method() + " " + url.pathname + url.search);
    const fulfill = (body: unknown, status = 200) =>
      route.fulfill({
        status,
        contentType: "application/json",
        body: JSON.stringify(body),
      });
    if (url.pathname === "/api/session") return route.fulfill({ status: 204 });
    if (url.pathname === "/api/registrations")
      return fulfill({
        accounts: [
          { source: "moysklad", account, registered_at: observed },
          ...(scenario === "missing"
            ? []
            : [{ source: "ozon", account, registered_at: observed }]),
        ],
        truncated: false,
      });
    if (url.pathname === "/api/diagnostics") return fulfill(report);
    if (/\/api\/integrations\/[^/]+\/credentials$/.test(url.pathname)) {
      const provider = url.pathname.split("/")[3];
      if (request.method() === "POST") {
        credentialBodies.push(request.postData() || "");
        const body = request.postDataJSON();
        credential = {
          ...credential,
          provider,
          revision: body.expected_revision + 1,
          enabled: true,
          last_request_id: body.request_id,
        };
        if (credentialBodies.length === 1) return route.abort("failed");
        return fulfill({ credential, idempotent_replay: true });
      }
      return fulfill({
        storage_available: true,
        credential: { ...credential, provider },
      });
    }
    if (url.pathname === "/api/stock-sync") return fulfill(sync);
    if (url.pathname === "/api/stock-sync/preview")
      return fulfill({
        ...sync,
        ready: true,
        review_id: "c".repeat(32),
        created_at: observed,
        expires_at: "2026-10-29T06:46:00Z",
        checks: [],
        targets: [
          {
            ozon_product_id: "100",
            ozon_warehouse_id: "10",
            source_item_id: "p1",
            source_warehouse_id: "w1",
            quantity: "4",
            status: "READY",
            checks: [],
            product_mapping_version: 1,
            warehouse_mapping_version: 1,
          },
        ],
      });
    if (
      url.pathname === "/api/stock-sync/enable" ||
      url.pathname === "/api/stock-sync/disable"
    ) {
      syncBodies.push(request.postData() || "");
      sync = {
        ...sync,
        enabled: url.pathname.endsWith("enable"),
        state: url.pathname.endsWith("enable") ? "QUEUED" : "DISABLED",
      };
      return fulfill(sync);
    }
    if (url.pathname === "/api/integrations")
      return fulfill({
        providers: (["moysklad", "ozon"] as const).map((provider) => ({
          provider,
          account,
          status:
            provider === "ozon" && scenario === "missing"
              ? "CONFIGURED_NOT_VERIFIED"
              : provider === "moysklad" &&
                  ["history-unavailable", "key-error"].includes(scenario)
                ? "VERIFICATION_FAILED"
                : "VERIFIED",
          registration:
            provider === "ozon" && scenario === "missing"
              ? "NOT_REGISTERED"
              : "REGISTERED",
          ...(provider === "ozon" && scenario === "missing"
            ? {}
            : {
                check: {
                  reason:
                    scenario === "key-error" && provider === "moysklad"
                      ? "INVALID_CREDENTIALS"
                      : undefined,
                  checked_at:
                    scenario === "history-unavailable" && provider === "ozon"
                      ? "2026-10-29T05:35:00Z"
                      : observed,
                  identity:
                    provider === "moysklad"
                      ? "MATCHED_RETURNED_PRODUCT"
                      : "NOT_ESTABLISHED",
                },
              }),
          import: report.batches[provider],
        })),
      });
    if (url.pathname === "/api/imports" && request.method() === "POST") {
      submissions.push(request.postData() || "");
      if (scenario === "unknown-submission") return route.abort("failed");
      return fulfill({
        operation: operations.moysklad,
        idempotent_replay: false,
      });
    }
    if (url.pathname === "/api/imports") {
      const provider = url.searchParams.has("moysklad_account")
        ? "moysklad"
        : "ozon";
      if (provider === "moysklad" && scenario === "history-unavailable")
        return fulfill({ error: "SYNTHETIC_HISTORY_UNAVAILABLE" }, 503);
      const offset = Number(url.searchParams.get("offset") || 0);
      return fulfill({
        items: offset === 0 ? [operations[provider]] : [],
        has_next: offset === 0,
        offset,
      });
    }
    if (url.pathname.startsWith("/api/imports/")) {
      return fulfill(
        url.pathname.endsWith(operations.moysklad.id)
          ? operations.moysklad
          : operations.ozon,
      );
    }
    return fulfill({ error: "UNEXPECTED_SYNTHETIC_REQUEST" }, 501);
  });
  await page.goto(origin + "/integrations");
  await expect(
    page.getByRole("heading", { name: "Интеграции", exact: true }),
  ).toBeVisible();
  await expect(page.locator(".integration-card")).toHaveCount(2);
  return { calls, submissions, credentialBodies, syncBodies };
}
async function assertLayout(page: Page, identifiersHidden = true) {
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  if (identifiersHidden) {
    const text = await page.locator("body").innerText();
    expect(text).not.toContain(account);
    expect(text).not.toContain(tenant);
    expect(text).not.toMatch(/a{32}|b{32}|COMPLETE_TRAVERSAL|READING_PRODUCTS/);
  }
  expect(await page.content()).not.toContain("synthetic-secret-entered");
}
const sizes = [
  { width: 1440, height: 900 },
  { width: 1280, height: 800 },
  { width: 390, height: 844 },
];
const shot = async (page: Page, name: string) =>
  page.screenshot({
    path: `${process.env.STYK_VISUAL_SCREENSHOTS_DIR || "../docs/screenshots/integrations-reference"}/${name}.png`,
    fullPage: (await page.getByRole("dialog").count()) === 0,
  });
for (const size of sizes) {
  test(`reference layout ${size.width}`, async ({ page }) => {
    await page.setViewportSize(size);
    const { calls, submissions } = await openFixture(page);
    await expect(
      page.getByRole("button", { name: "Обновить данные", exact: true }),
    ).toHaveCount(2);
    await expect(
      page.getByRole("link", { name: "Сопоставить товары", exact: true }),
    ).toBeVisible();
    if (size.width > 1000) {
      for (const action of await page.locator(".import-primary").all()) {
        const box = await action.boundingBox();
        expect(box!.y + box!.height).toBeLessThan(size.height);
      }
    }
    await assertLayout(page);
    expect(submissions).toHaveLength(0);
    expect(
      calls
        .filter((c) => c.includes("/api/imports?"))
        .every((c) => c.includes("limit=10")),
    ).toBe(true);
    await shot(page, `success-${size.width}`);
  });
  test(`settings and technical disclosure preserve geometry ${size.width}`, async ({
    page,
  }) => {
    await page.setViewportSize(size);
    await openFixture(page);
    const cards = page.locator(".integration-cards");
    const geometry = () =>
      cards.evaluate((el) => {
        const r = el.getBoundingClientRect();
        return {
          x: r.x + scrollX,
          y: r.y + scrollY,
          width: r.width,
          height: r.height,
        };
      });
    const before = await geometry();
    const settings = page
      .getByRole("article", { name: "Ozon", exact: true })
      .getByRole("button", { name: "Настройки", exact: true });
    await settings.click();
    const dialog = page.getByRole("dialog", { name: "Подключение Ozon" });
    await expect(dialog).toBeVisible();
    expect((await dialog.boundingBox())!.width).toBeLessThanOrEqual(
      Math.min(480, size.width),
    );
    expect(await geometry()).toEqual(before);
    await shot(page, `settings-${size.width}`);
    await dialog.locator(".technical-details > summary").first().click();
    await expect(
      dialog.getByText(account, { exact: true }).first(),
    ).toBeVisible();
    await dialog
      .getByRole("button", { name: "Копировать идентификаторы" })
      .first()
      .scrollIntoViewIfNeeded();
    await expect(
      dialog.getByRole("button", { name: "Копировать идентификаторы" }).first(),
    ).toBeInViewport();
    expect(await geometry()).toEqual(before);
    await assertLayout(page, false);
    await shot(page, `technical-${size.width}`);
    await page.keyboard.press("Escape");
    await expect(dialog).toHaveCount(0);
    await expect(settings).toBeFocused();
    expect(await geometry()).toEqual(before);
  });
  test(`explicit import confirmation ${size.width}`, async ({ page }) => {
    await page.setViewportSize(size);
    const { submissions } = await openFixture(page);
    await page
      .getByRole("article", { name: "МойСклад", exact: true })
      .getByRole("button", { name: "Обновить данные", exact: true })
      .click();
    const dialog = page.getByRole("dialog", {
      name: "Загрузить данные МойСклад?",
    });
    await expect(
      dialog.getByRole("button", { name: "Подтвердить загрузку" }),
    ).toBeDisabled();
    expect(submissions).toHaveLength(0);
    await assertLayout(page);
    await shot(page, `import-confirmation-${size.width}`);
    await page.keyboard.press("Escape");
    expect(submissions).toHaveLength(0);
  });
}
for (const scenario of [
  "missing",
  "running",
  "failed",
  "unknown-operation",
  "history-unavailable",
  "key-error",
  "sync-active",
] as const) {
  test(`state ${scenario}`, async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    await openFixture(page, scenario);
    if (scenario === "missing")
      await expect(
        page.getByRole("link", { name: "Сопоставить товары", exact: true }),
      ).toHaveCount(0);
    if (scenario === "running") {
      await expect(
        page.getByText("Загружаем товары…", { exact: true }).first(),
      ).toBeVisible();
      await expect(page.getByText(/Обнаружено.*842/).first()).toBeVisible();
    }
    if (scenario === "key-error")
      await expect(
        page.getByRole("button", { name: "Обновить ключ", exact: true }),
      ).toBeVisible();
    if (scenario === "unknown-operation")
      await expect(
        page.getByText("Результат уточняется", { exact: true }).first(),
      ).toBeVisible();
    if (scenario === "sync-active")
      await expect(
        page
          .getByRole("region", { name: "Синхронизация остатков" })
          .getByText("Включена", { exact: true }),
      ).toBeVisible();
    await assertLayout(page);
    await shot(page, `${scenario}-1440`);
  });
}
test("unknown import submission preserves exact replay", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  const { submissions } = await openFixture(page, "unknown-submission");
  await page
    .getByRole("article", { name: "МойСклад", exact: true })
    .getByRole("button", { name: "Обновить данные", exact: true })
    .click();
  const dialog = page.getByRole("dialog");
  await dialog.getByRole("checkbox").check();
  await dialog.getByRole("button", { name: "Подтвердить загрузку" }).click();
  await expect(
    page.getByRole("button", { name: "Повторить тот же запрос", exact: true }),
  ).toBeVisible();
  await page
    .getByRole("button", { name: "Повторить тот же запрос", exact: true })
    .click();
  await expect.poll(() => submissions.length).toBe(2);
  expect(submissions[1]).toBe(submissions[0]);
  await assertLayout(page);
  await shot(page, "unknown-submission-1440");
});
test("bounded history and support details", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  const { calls } = await openFixture(page);
  await page
    .getByRole("button", { name: "История обновлений", exact: true })
    .click();
  const dialog = page.getByRole("dialog", {
    name: "История обновлений",
    exact: true,
  });
  const history = dialog.locator(
    'details.provider-history[aria-label="МойСклад: история обновлений"]',
  );
  await history.locator(":scope > summary").click();
  await history.locator("details.history-entry > summary").first().click();
  await history.locator("details.technical-details > summary").first().click();
  await expect(
    history.getByText("a".repeat(32), { exact: true }).first(),
  ).toBeVisible();
  await assertLayout(page, false);
  await shot(page, "operation-details-1440");
  await history.getByRole("button", { name: /Следующ/ }).click();
  await expect
    .poll(() => calls.some((c) => c.includes("offset=10")))
    .toBe(true);
  await page.keyboard.press("Escape");
  await page.getByRole("button", { name: "Диагностика для поддержки" }).click();
  await expect(
    page.getByRole("dialog", { name: "Диагностика", exact: true }),
  ).toBeVisible();
  await shot(page, "support-1440");
});
test("credential save is explicit, secret stays out of HTML, lost response retries identical body", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  const { credentialBodies, submissions } = await openFixture(
    page,
    "key-error",
  );
  await page
    .getByRole("button", { name: "Обновить ключ", exact: true })
    .click();
  const dialog = page.getByRole("dialog", { name: "Подключение МойСклад" });
  await dialog
    .locator('input[type="password"]')
    .fill("synthetic-secret-entered");
  await assertLayout(page);
  const save = dialog.getByRole("button", {
    name: /Проверить и сохранить|Сохранить ключ|Сохранить и проверить/,
  });
  await expect(save).toBeDisabled();
  await dialog
    .getByRole("checkbox", { name: /Подтверждаю, что этот аккаунт/ })
    .check();
  await shot(page, "update-key-1440");
  await save.click();
  await expect(
    page
      .getByRole("button", { name: "Повторить тот же запрос", exact: true })
      .last(),
  ).toBeVisible();
  await page
    .getByRole("button", { name: "Повторить тот же запрос", exact: true })
    .last()
    .click();
  await expect.poll(() => credentialBodies.length).toBe(2);
  expect(credentialBodies[1]).toBe(credentialBodies[0]);
  expect(JSON.parse(credentialBodies[0]).request_id).toMatch(/^[0-9a-f-]{36}$/);
  expect(submissions).toHaveLength(0);
  expect(
    await page.evaluate(() =>
      JSON.stringify({ ...localStorage, ...sessionStorage }),
    ),
  ).not.toContain("synthetic-secret-entered");
});
test("stock publication requires separate preview and explicit acknowledgement", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  const { syncBodies, submissions } = await openFixture(page, "sync-ready");
  await page
    .getByRole("button", { name: "Включить синхронизацию", exact: true })
    .click();
  const dialog = page.getByRole("dialog", {
    name: "Включить синхронизацию остатков?",
  });
  const enable = dialog.getByRole("button", {
    name: "Включить синхронизацию",
    exact: true,
  });
  await expect(enable).toBeDisabled();
  expect(syncBodies).toHaveLength(0);
  await expect(dialog.getByText(/Условия отправки подтверждены/)).toBeVisible();
  await shot(page, "enable-sync-1440");
  await dialog
    .getByRole("checkbox", { name: /Подтверждаю изменение остатков/ })
    .check();
  await enable.click();
  await expect.poll(() => syncBodies.length).toBe(1);
  expect(JSON.parse(syncBodies[0])).toMatchObject({
    tenant,
    moysklad_account: account,
    ozon_account: account,
    review_id: "c".repeat(32),
    acknowledge: true,
  });
  expect(submissions).toHaveLength(0);
  await expect(
    page
      .getByRole("region", { name: "Синхронизация остатков" })
      .getByText("Идёт отправка", { exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Выключить", exact: true }).click();
  const disable = page.getByRole("dialog", {
    name: "Выключить синхронизацию?",
  });
  await expect(
    disable.getByRole("button", {
      name: "Выключить синхронизацию",
      exact: true,
    }),
  ).toBeDisabled();
  await disable.getByRole("checkbox").check();
  await disable
    .getByRole("button", { name: "Выключить синхронизацию", exact: true })
    .click();
  await expect.poll(() => syncBodies.length).toBe(2);
  await expect(
    page
      .getByRole("region", { name: "Синхронизация остатков" })
      .getByText("Выключена", { exact: true }),
  ).toBeVisible();
});

test("deployment safety hold overrides a previously enabled policy", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1280, height: 800 });
  const { syncBodies } = await openFixture(page, "sync-hold");
  const card = page.getByRole("region", { name: "Синхронизация остатков" });
  await expect(card.getByText("Недоступна", { exact: true })).toBeVisible();
  await expect(
    card.getByText(/проверка безопасности ещё не завершена/),
  ).toBeVisible();
  await expect(card.getByRole("button", { name: "Выключить" })).toBeVisible();
  await expect(
    page.getByText("Все шаги выполнены", { exact: true }),
  ).toHaveCount(0);
  expect(syncBodies).toHaveLength(0);
  await assertLayout(page);
  await shot(page, "publication-safety-hold");
});
