import { expect, test, type Locator, type Page } from "@playwright/test";
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
  | "unknown-operation";
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
    mapping_counts: { products: 0, warehouses: 0 },
  };
  const calls: string[] = [];
  const submissions: string[] = [];
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
    if (url.pathname === "/api/integrations")
      return fulfill({
        providers: (["moysklad", "ozon"] as const).map((provider) => ({
          provider,
          account,
          status:
            provider === "ozon" && scenario === "missing"
              ? "CONFIGURED_NOT_VERIFIED"
              : provider === "moysklad" && scenario === "history-unavailable"
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
  return { calls, submissions };
}

function providerCard(page: Page, name: "МойСклад" | "Ozon") {
  return page.getByRole("article", { name, exact: true });
}

async function geometry(locator: Locator) {
  return locator.evaluate((element) => {
    const rect = element.getBoundingClientRect();
    return {
      x: rect.x + scrollX,
      y: rect.y + scrollY,
      width: rect.width,
      height: rect.height,
    };
  });
}

async function expectUnchangedGeometry(
  locator: Locator,
  before: Awaited<ReturnType<typeof geometry>>,
) {
  const after = await geometry(locator);
  for (const property of ["x", "y", "width", "height"] as const)
    expect(Math.abs(after[property] - before[property]), property).toBeLessThan(
      1.5,
    );
}

async function expectActionNearContent(card: Locator) {
  const gap = await card.locator(".import-primary").evaluate((button) => {
    const previous = button.previousElementSibling!;
    return (
      button.getBoundingClientRect().top -
      previous.getBoundingClientRect().bottom
    );
  });
  expect(gap).toBeGreaterThanOrEqual(0);
  expect(gap).toBeLessThan(64);
}

async function expectDrawerFillsViewport(drawer: Locator, height: number) {
  const bounds = await drawer.boundingBox();
  expect(bounds).not.toBeNull();
  expect(bounds!.y).toBe(0);
  expect(bounds!.height).toBe(height);
}

async function expectDrawerBottomReachable(drawer: Locator, height: number) {
  const copy = drawer.getByRole("button", {
    name: "Копировать идентификаторы",
  });
  await copy.scrollIntoViewIfNeeded();
  await expect(copy).toBeInViewport();
  await expect(
    drawer.getByRole("button", { name: "Закрыть настройки" }),
  ).toBeInViewport();
  const scroll = await drawer
    .locator(".provider-settings-dialog-content")
    .evaluate((element) => ({
      top: element.scrollTop,
      hasOverflow: element.scrollHeight > element.clientHeight,
    }));
  if (scroll.hasOverflow) expect(scroll.top).toBeGreaterThan(0);
  await expectDrawerFillsViewport(drawer, height);
}

async function assertLayout(page: Page) {
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  const visible = await page.locator("body").innerText();
  expect(visible).not.toContain(account);
  expect(visible).not.toContain(tenant);
  expect(visible).not.toMatch(/a{32}|b{32}/);
  expect(visible).not.toMatch(
    /COMPLETE_TRAVERSAL|READING_PRODUCTS|CONFIGURED_NOT_VERIFIED|HTTP attempts/,
  );
  expect(visible).not.toContain("synthetic-import-key");
  expect(visible).not.toContain("synthetic-moy-import-token");
}

for (const viewport of [
  { width: 1440, height: 900 },
  { width: 1280, height: 800 },
  { width: 390, height: 844 },
]) {
  test(`both imports complete at ${viewport.width} × ${viewport.height}`, async ({
    page,
  }) => {
    await page.setViewportSize(viewport);
    const { calls, submissions } = await openFixture(page);
    await expect(
      page.getByText("Данные обеих систем загружены", { exact: true }),
    ).toBeVisible();
    await expect(
      page.getByRole("button", { name: "Обновить данные", exact: true }),
    ).toHaveCount(2);
    await expect(
      page.getByRole("link", { name: "Сопоставить товары", exact: true }),
    ).toBeVisible();
    await expect(
      page.getByRole("link", { name: "Сопоставить склады", exact: true }),
    ).toBeVisible();
    if (viewport.width > 1000) {
      for (const action of await page
        .getByRole("button", { name: "Обновить данные", exact: true })
        .all()) {
        const box = await action.boundingBox();
        expect(box && box.y + box.height).toBeLessThan(viewport.height);
      }
      const next = await page
        .getByText("Данные обеих систем загружены", { exact: true })
        .boundingBox();
      expect(next && next.y + next.height).toBeLessThan(viewport.height);
    }
    await assertLayout(page);
    expect(submissions).toHaveLength(0);
    expect(
      calls
        .filter((call) => call.includes("/api/imports?"))
        .every((call) => call.includes("limit=10")),
    ).toBe(true);
    await page.screenshot({
      path: `test-results/integrations-ux/success-${viewport.width}.png`,
      fullPage: true,
    });
  });
}

for (const scenario of [
  "missing",
  "running",
  "failed",
  "unknown-operation",
] as const) {
  test(`integration state: ${scenario}`, async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    await openFixture(page, scenario);
    if (scenario === "missing") {
      await expect(
        page.getByText("Данные обеих систем загружены", { exact: true }),
      ).toHaveCount(0);
      await expect(
        page.getByRole("link", { name: "Сопоставить товары", exact: true }),
      ).toHaveCount(0);
    } else if (scenario === "running") {
      await expect(
        page.getByText("Загружаем товары…", { exact: true }).first(),
      ).toBeVisible();
      await expect(page.getByText(/Обнаружено.*842/).first()).toBeVisible();
      await expect(
        page
          .getByRole("region", { name: "МойСклад: импорт данных" })
          .getByRole("button", { name: "Обновить данные", exact: true }),
      ).toBeDisabled();
    } else if (scenario === "failed") {
      await expect(
        page
          .getByText(
            /Предыдущие.*(данные|загрузки)|предыдущие.*(данные|загрузки)/,
          )
          .first(),
      ).toBeVisible();
    } else {
      await expect(
        page.getByText("Результат уточняется", { exact: true }).first(),
      ).toBeVisible();
      await expect(
        page
          .getByRole("region", { name: "МойСклад: импорт данных" })
          .getByRole("button", { name: "Обновить данные", exact: true }),
      ).toBeDisabled();
    }
    await assertLayout(page);
    await page.screenshot({
      path: `test-results/integrations-ux/${scenario}-1440.png`,
      fullPage: true,
    });
  });
}

test("unknown submission preserves a visible recovery action", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  const { submissions } = await openFixture(page, "unknown-submission");
  const card = page.locator(".integration-card").filter({
    has: page.getByRole("heading", { name: "МойСклад", exact: true }),
  });
  await card
    .getByRole("button", { name: "Обновить данные", exact: true })
    .click();
  const dialog = page.getByRole("dialog");
  await dialog.getByRole("checkbox").check();
  await dialog.getByRole("button", { name: /Подтвердить/ }).click();
  await expect(
    page.getByRole("button", { name: /Повторить тот же запрос/ }),
  ).toBeVisible();
  await assertLayout(page);
  expect(submissions).toHaveLength(1);
  await page.screenshot({
    path: "test-results/integrations-ux/unknown-submission-1440.png",
    fullPage: true,
  });
});

test("history reveals technical details only on request", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  const { calls } = await openFixture(page);
  const history = page.locator(
    'details.provider-history[aria-label="МойСклад: история обновлений"]',
  );
  await history.locator(":scope > summary").click();
  await history.locator("details.history-entry > summary").first().click();
  await history.locator("details.technical-details > summary").first().click();
  await expect(
    page.getByText("a".repeat(32), { exact: true }).first(),
  ).toBeVisible();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  await page.screenshot({
    path: "test-results/integrations-ux/technical-details-1440.png",
    fullPage: true,
  });
  await page
    .getByRole("button", { name: /Следующ/ })
    .first()
    .click();
  await expect
    .poll(() => calls.some((call) => call.includes("offset=10")))
    .toBe(true);
  expect(
    calls
      .filter((call) => call.includes("/api/imports?"))
      .every((call) => call.includes("limit=10")),
  ).toBe(true);
});

for (const viewport of [
  { width: 1440, height: 900 },
  { width: 1280, height: 800 },
  { width: 390, height: 844 },
]) {
  test(`provider settings drawer never reflows the page at ${viewport.width}px`, async ({
    page,
  }) => {
    await page.setViewportSize(viewport);
    const { submissions } = await openFixture(page);
    const moy = providerCard(page, "МойСклад"),
      ozon = providerCard(page, "Ozon");
    const moyAction = moy.locator(".import-primary"),
      ozonAction = ozon.locator(".import-primary");
    await expect(moyAction).toBeEnabled();
    await expect(ozonAction).toBeEnabled();
    const stableElements = [
      moy,
      moyAction,
      ozon,
      ozonAction,
      page.locator(".integration-next"),
      page.locator(".updates-history"),
    ];
    const baseline = await Promise.all(stableElements.map(geometry));
    const expectNoReflow = async () => {
      for (let i = 0; i < stableElements.length; i++)
        await expectUnchangedGeometry(stableElements[i], baseline[i]);
    };

    await ozon.locator(".provider-settings > summary").click();
    const ozonDrawer = page.getByRole("dialog", {
      name: "Настройки Ozon",
      exact: true,
    });
    await expect(ozonDrawer).toBeVisible();
    await expectDrawerFillsViewport(ozonDrawer, viewport.height);
    await expect(page.getByRole("dialog")).toHaveCount(1);
    await expect(
      ozonDrawer.getByRole("combobox", { name: "Ozon: аккаунт" }),
    ).toBeVisible();
    await expectNoReflow();
    await expectActionNearContent(moy);
    await expectActionNearContent(ozon);
    await assertLayout(page);
    await page.screenshot({
      path: `test-results/integrations-ux/expanded-settings-${viewport.width}.png`,
      fullPage: false,
    });

    // The first and last keyboard stops stay inside the modal.
    const ozonClose = ozonDrawer.getByRole("button", {
      name: "Закрыть настройки",
    });
    await ozonClose.focus();
    await page.keyboard.press("Shift+Tab");
    expect(
      await ozonDrawer.evaluate((node) =>
        node.contains(document.activeElement),
      ),
    ).toBe(true);
    await page.keyboard.press("Tab");
    await expect(ozonClose).toBeFocused();
    await ozonDrawer.locator(".technical-details > summary").click();
    await expect(
      ozonDrawer.getByText(account, { exact: true }).first(),
    ).toBeVisible();
    await expectDrawerBottomReachable(ozonDrawer, viewport.height);
    await expectNoReflow();
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBe(true);
    await page.screenshot({
      path: `test-results/integrations-ux/expanded-provider-details-${viewport.width}.png`,
      fullPage: false,
    });
    await page.keyboard.press("Escape");
    await expect(ozonDrawer).toHaveCount(0);
    await expect(ozon.locator(".provider-settings > summary")).toBeFocused();
    await expectNoReflow();

    await moy.locator(".provider-settings > summary").click();
    const moyDrawer = page.getByRole("dialog", {
      name: "Настройки МойСклад",
      exact: true,
    });
    await expect(moyDrawer).toBeVisible();
    await expectDrawerFillsViewport(moyDrawer, viewport.height);
    await expect(page.getByRole("dialog")).toHaveCount(1);
    await moyDrawer.locator(".technical-details > summary").click();
    await expect(
      moyDrawer.getByText(account, { exact: true }).first(),
    ).toBeVisible();
    await expectDrawerBottomReachable(moyDrawer, viewport.height);
    await expectNoReflow();
    await page.screenshot({
      path: `test-results/integrations-ux/expanded-moy-settings-${viewport.width}.png`,
      fullPage: false,
    });
    await moyDrawer.getByRole("button", { name: "Закрыть настройки" }).click();
    await expect(moyDrawer).toHaveCount(0);
    await expect(moy.locator(".provider-settings > summary")).toBeFocused();
    await expectNoReflow();

    if (viewport.width > 1000) {
      await ozon.locator(".provider-settings > summary").click();
      await expect(ozonDrawer).toBeVisible();
      await page.mouse.click(10, 100);
      await expect(ozonDrawer).toHaveCount(0);
      await expect(ozon.locator(".provider-settings > summary")).toBeFocused();
    }
    await assertLayout(page);
    await expectNoReflow();
    expect(submissions).toHaveLength(0);
  });

  test(`import confirmation never reflows the page at ${viewport.width}px`, async ({
    page,
  }) => {
    await page.setViewportSize(viewport);
    const { submissions } = await openFixture(page);
    const moy = providerCard(page, "МойСклад"),
      ozon = providerCard(page, "Ozon");
    await expect(moy.locator(".import-primary")).toBeEnabled();
    await expect(ozon.locator(".import-primary")).toBeEnabled();
    const stableElements = [
      moy,
      moy.locator(".import-primary"),
      ozon,
      ozon.locator(".import-primary"),
      page.locator(".integrations-heading"),
      page.locator(".integration-cards"),
      page.locator(".integration-next"),
      page.locator(".updates-history"),
    ];
    const baseline = await Promise.all(stableElements.map(geometry));
    await moy.locator(".import-primary").click();
    const confirmation = moy.getByRole("dialog", {
      name: "Загрузить данные МойСклад?",
    });
    await expect(confirmation).toBeVisible();
    await expectDrawerFillsViewport(confirmation, viewport.height);
    await expect(
      confirmation.getByRole("button", { name: "Подтвердить загрузку" }),
    ).toBeDisabled();
    for (let i = 0; i < stableElements.length; i++)
      await expectUnchangedGeometry(stableElements[i], baseline[i]);
    await expectActionNearContent(moy);
    await expectActionNearContent(ozon);
    await assertLayout(page);
    expect(submissions).toHaveLength(0);
    await page.screenshot({
      path: `test-results/integrations-ux/import-confirmation-${viewport.width}.png`,
      fullPage: false,
    });
    await confirmation
      .getByRole("button", { name: "Закрыть", exact: true })
      .click();
    await expect(confirmation).toHaveCount(0);
    for (let i = 0; i < stableElements.length; i++)
      await expectUnchangedGeometry(stableElements[i], baseline[i]);
    expect(submissions).toHaveLength(0);
  });

  test(`unavailable history stays compact behind settings drawer at ${viewport.width}px`, async ({
    page,
  }) => {
    await page.setViewportSize(viewport);
    const { submissions } = await openFixture(page, "history-unavailable");
    const moy = providerCard(page, "МойСклад"),
      ozon = providerCard(page, "Ozon");
    await expect(moy.getByRole("alert")).toContainText("История недоступна");
    await expect(moy.locator(".import-primary")).toBeDisabled();
    await expect(ozon.locator(".import-primary")).toBeEnabled();
    const stableElements = [
      moy,
      moy.locator(".import-primary"),
      ozon,
      ozon.locator(".import-primary"),
      page.locator(".integration-next"),
      page.locator(".updates-history"),
    ];
    const baseline = await Promise.all(stableElements.map(geometry));
    await ozon.locator(".provider-settings > summary").click();
    const drawer = page.getByRole("dialog", {
      name: "Настройки Ozon",
      exact: true,
    });
    await expect(drawer).toBeVisible();
    await expectDrawerFillsViewport(drawer, viewport.height);
    await drawer.locator(".technical-details > summary").click();
    await expect(
      drawer.getByText(/Перед добавлением аккаунта повторите проверку/),
    ).toHaveCount(0);
    await expect(
      drawer.getByText(account, { exact: true }).first(),
    ).toBeVisible();
    await expectDrawerBottomReachable(drawer, viewport.height);
    for (let i = 0; i < stableElements.length; i++)
      await expectUnchangedGeometry(stableElements[i], baseline[i]);
    await expectActionNearContent(moy);
    await expectActionNearContent(ozon);
    await expect(moy.locator(".import-primary")).toBeDisabled();
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBe(true);
    expect(submissions).toHaveLength(0);
    await page.screenshot({
      path: `test-results/integrations-ux/error-history-expanded-settings-${viewport.width}.png`,
      fullPage: false,
    });
    await drawer.getByRole("button", { name: "Закрыть настройки" }).click();
    for (let i = 0; i < stableElements.length; i++)
      await expectUnchangedGeometry(stableElements[i], baseline[i]);
  });
}
