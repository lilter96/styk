import { test, expect, type Page } from "@playwright/test";
import { auditButtonHovers } from "./hover-contrast";

test("new merchant imports all eligible Ozon products with one confirmation", async ({
  page,
}) => {
  await fixtures(page);
  const source = (id: string, archived = false) => ({
    identity: {
      account: { provider: "ozon", id: scope.ozon_account },
      entity_type: "product",
      id,
      offer_id: `seller-${id}`,
    },
    fields: { name: { state: "known", data: `Товар ${id}` } },
    revision: "source-1",
    lifecycle: archived ? "archived" : "active",
  });
  const mutations: string[] = [];
  await page.route("**/api/catalog-sync/**", async (route) => {
    const request = route.request(),
      url = new URL(request.url());
    if (request.method() === "POST") mutations.push(url.pathname);
    if (url.pathname.endsWith("config"))
      return route.fulfill({
        json: {
          scope,
          revision: 0,
          enabled: false,
          test_enabled: false,
          targets: [],
          rules: {},
          publication_available: true,
        },
      });
    if (url.pathname.endsWith("products"))
      return route.fulfill({
        json: url.searchParams.get("cursor")
          ? { products: [source("1002"), source("1003", true)] }
          : { products: [source("1001")], next_cursor: "second-page" },
      });
    if (url.pathname.endsWith("operations"))
      return route.fulfill({ json: { operations: [] } });
    const body = request.postDataJSON();
    if (url.pathname.endsWith("import-preview"))
      return route.fulfill({
        json: {
          id: "batch-preview",
          scope,
          config_revision: 1,
          expires_at: "2099-01-01T00:00:00Z",
          decisions: body.targets.map(
            (
              target: {
                source: { id: string };
                target: unknown;
                create: { values: unknown };
              },
              i: number,
            ) => ({
              enrollment_id: `frozen-${i}`,
              source: source(target.source.id),
              target: { identity: target.target },
              target_absent: true,
              ready: true,
              validation: [],
              plan: { conflicts: [], changes: [] },
              commands: [
                {
                  action: "create",
                  target: target.target,
                  values: target.create.values,
                },
              ],
            }),
          ),
        },
      });
    if (url.pathname.endsWith("approve")) {
      expect(body.enrollment_ids).toEqual(["frozen-0", "frozen-1"]);
      return route.fulfill({
        json: {
          id: "batch-operation",
          scope,
          state: "QUEUED",
          targets: [{}, {}],
        },
      });
    }
    return route.fulfill({ status: 404, json: {} });
  });
  let savedDraft = { revision: 0, state: {} };
  await page.route("**/api/product-import-draft*", async (route) => {
    if (route.request().method() === "POST")
      savedDraft = {
        revision: savedDraft.revision + 1,
        state: route.request().postDataJSON().state,
      };
    await route.fulfill({ json: savedDraft });
  });
  await page.goto(origin + "/products/import/ozon-to-moysklad");
  await page.getByLabel("Выбрать Товар 1001", { exact: true }).check();
  await page.getByRole("button", { name: "Далее", exact: true }).click();
  await page.getByLabel("Выбрать Товар 1002", { exact: true }).check();
  await expect(
    page.getByLabel("Выбрать Товар 1003", { exact: true }),
  ).toBeDisabled();
  await page.getByRole("button", { name: "Проверить выбранные (2)" }).click();
  await page.getByLabel("Выбрать Товар 1001", { exact: true }).check();
  await page.getByLabel("Выбрать Товар 1002", { exact: true }).check();
  await page.getByRole("button", { name: "Продолжить с 2 товарами →" }).click();
  await expect(
    page.getByRole("button", { name: "Отправить изменения (2)" }),
  ).toBeDisabled();
  await page.getByLabel(/Я проверил изменения выбранных товаров/).check();
  await page.getByRole("button", { name: "Отправить изменения (2)" }).click();
  await expect(
    page.getByRole("heading", { name: "Запрос поставлен в обработку" }),
  ).toBeVisible();
  expect(mutations).toEqual([
    "/api/catalog-sync/import-preview",
    "/api/catalog-sync/approve",
  ]);
});

// Presentation fixtures are confined to this test. Application requests and
// production data flow are unchanged. These checks do not replace API e2e.
const origin = process.env.STYK_UI_VISUAL_ORIGIN || "http://127.0.0.1:5173";
const batch = {
  present: true,
  completed: true,
  observed_at: new Date().toISOString(),
  age: "1m",
  counts: { products: 128, variants: 42, warehouses: 2, stock_rows: 256 },
};
const scope = {
  tenant: "visual-workspace",
  moysklad_account: "visual-moy",
  ozon_account: "visual-ozon",
};
const accounts = [
  {
    source: "moysklad",
    account: "visual-moy",
    name: "Основной склад",
    registered_at: "2026-01-01",
  },
  {
    source: "ozon",
    account: "visual-ozon",
    name: "STYK · Основной магазин",
    registered_at: "2026-01-01",
  },
  {
    source: "ozon",
    account: "visual-ozon-2",
    name: "STYK · Второй магазин с длинным названием",
    registered_at: "2026-01-02",
  },
];
const report = {
  batches: { moysklad: batch, ozon: batch },
  checks: [
    { code: "MOYSKLAD_IMPORT", status: "PASS", detail: "Completed" },
    { code: "OZON_IMPORT", status: "PASS", detail: "Completed" },
    { code: "PRODUCT_MAPPINGS", status: "WARN", detail: "Review required" },
    { code: "WAREHOUSE_MAPPINGS", status: "PASS", detail: "Completed" },
  ],
  decision_status: "UNKNOWN",
  decision_reasons: [],
  publication: "DISABLED",
  mapping_counts: {
    confirmed_products: 96,
    unconfirmed_products: 32,
    catalog_products: 128,
    catalog_confirmed_products: 96,
    confirmed_warehouses: 1,
  },
};

async function fixtures(page: Page, signedIn = true) {
  await page.addInitScript(() =>
    sessionStorage.setItem(
      "styk-selected-accounts",
      JSON.stringify({
        tenant: "visual-workspace",
        moy: "visual-moy",
        ozon: "visual-ozon",
      }),
    ),
  );
  await page.route("**/api/**", async (route) => {
    const url = new URL(route.request().url());
    const path = url.pathname;
    if (route.request().method() !== "GET") {
      await route.fulfill({
        status: 405,
        json: { error: "VISUAL_TEST_READ_ONLY" },
      });
      return;
    }
    if (path === "/api/session") {
      await route.fulfill(
        signedIn
          ? {
              json: {
                user: { id: "visual-user", email: "seller@example.test" },
                account_id: scope.tenant,
                csrf: "visual-only",
                expires_at: "2099-01-01T00:00:00Z",
              },
            }
          : { status: 401, json: {} },
      );
      return;
    }
    let value: unknown;
    if (path === "/api/auth-mode") value = { mode: "merchant" };
    else if (path === "/api/registrations")
      value = { accounts, truncated: false };
    else if (path === "/api/diagnostics") value = report;
    else if (path === "/api/products")
      value = {
        items: Array.from({ length: 8 }, (_, index) => ({
          product_id: `product-${index}`,
          offer_id: `STYK-${1000 + index}`,
          name: `Органайзер для рабочего стола, модель ${index + 1}`,
          sku: `sku-${index}`,
          archived: false,
          target_kind: "product",
          target_id: `moy-${index}`,
          target_name: `Органайзер для рабочего стола ${index + 1}`,
          provider: "ozon" as const,
          kind: "product",
          observed: true,
          shared_inventory: false,
          match_status: "CONFIRMED",
          candidate_count: 0,
          candidates: [],
          candidates_truncated: false,
        })),
        has_next: true,
        offset: 0,
        total: 128,
        page: Number(url.searchParams.get("page") || 1),
        pageSize: 20,
      };
    else if (path === "/api/moysklad-products")
      value = { items: [], has_next: false, offset: 0 };
    else if (path === "/api/warehouses")
      value =
        url.searchParams.get("view") === "candidates"
          ? {
              items: [
                { id: "m-free", name: "Склад Екатеринбург", available: true },
                { id: "m-busy", name: "Основной склад", available: false },
              ],
              total: 2,
              offset: 0,
              has_next: false,
            }
          : {
              items: Array.from({ length: 6 }, (_, i) => ({
                id: `warehouse-${i}`,
                name: [
                  "Склад Ozon Москва",
                  "Склад Ozon Санкт-Петербург",
                  "Склад Ozon Казань",
                  "Склад Ozon Екатеринбург",
                  "Склад Ozon Новосибирск",
                  "Склад Ozon Воронеж",
                ][i],
                is_rfbs: i === 4,
                warehouse_type: i === 4 ? "rfbs" : "fbs",
                store_id: i < 3 ? `m-${i}` : "",
                store_name:
                  i < 3
                    ? [
                        "STYK-IT-20261028-61ce4a31cbdd-warehouse-a",
                        "Склад СПб",
                        "STYK-IT-20261028-61ce4a31cbdd-warehouse-b",
                      ][i]
                    : "",
                state: i < 3 ? "mapped" : i === 4 ? "unsupported" : "unmapped",
                stock_rows: i < 2 ? 128 : 0,
                observed_at: i < 2 ? new Date().toISOString() : null,
                mapping_id: i < 3 ? i + 1 : null,
                confirmed_at: i < 3 ? new Date().toISOString() : null,
              })).filter(
                (row) =>
                  !url.searchParams.get("warehouse") ||
                  row.id === url.searchParams.get("warehouse"),
              ),
              counts: { total: 6, mapped: 3, unmapped: 2, attention: 1 },
              total: 6,
              offset: 0,
              has_next: false,
              ozon_imported: true,
              moy_imported: true,
            };
    else if (path === "/api/stock")
      value = {
        items: [
          {
            kind: "product",
            item_id: "STYK-1000",
            stock: "24",
            reserve: "2",
            incoming: "6",
            quantity: "22",
            present: "22",
            reserved: "2",
            free_stock: "20",
            observed_at: new Date().toISOString(),
          },
        ],
        has_next: false,
        offset: 0,
      };
    else if (path === "/api/integrations")
      value = {
        providers: ["moysklad", "ozon"].map((provider) => ({
          provider,
          name: provider === "ozon" ? "Ozon" : "МойСклад",
          account:
            provider === "ozon" ? scope.ozon_account : scope.moysklad_account,
          status: "VERIFIED",
          registration: "REGISTERED",
          import: batch,
          check: { checked_at: new Date().toISOString(), identity: "VERIFIED" },
        })),
      };
    else if (path.includes("credentials"))
      value = { storage_available: true, credential: null };
    else if (path === "/api/stock-sync/eligibility")
      value = { state: "CONTROLLED_TEST_DISABLED" };
    else if (path === "/api/stock-sync")
      value = { ...scope, enabled: false, version: 0, last_operation: null };
    else if (path === "/api/catalog-sync/config")
      value = {
        scope,
        revision: 1,
        enabled: false,
        test_enabled: false,
        publication_available: false,
        interval_seconds: 300,
        rules: {
          name: "MOYSKLAD_AUTHORITATIVE",
          description: "OZON_AUTHORITATIVE",
        },
        targets: [],
        updated_at: new Date().toISOString(),
      };
    else if (path === "/api/product-matching/settings")
      value = {
        revision: 0,
        exact_sku: true,
        names: true,
        auto_confirm: false,
        after_import: false,
      };
    else if (path === "/api/catalog-sync/products")
      value = { products: [], next_cursor: "" };
    else if (path === "/api/catalog-sync/operations")
      value = { operations: [], next_cursor: "" };
    else if (path === "/api/orders/config")
      value = {
        scope,
        revision: 1,
        enabled: false,
        test_enabled: false,
        publication_available: false,
        interval_seconds: 300,
        source_freshness_seconds: 300,
        preview_ttl_seconds: 300,
        field_mappings: [],
        status_mappings: [],
      };
    else if (path === "/api/orders/postings") value = { postings: [] };
    else if (path === "/api/orders/operations") value = { operations: [] };
    else if (path === "/api/imports")
      value = { items: [], has_next: false, offset: 0 };
    else {
      await route.fulfill({
        status: 404,
        json: { error: "UNAVAILABLE_IN_VISUAL_FIXTURE" },
      });
      return;
    }
    if (
      path === "/api/warehouses" &&
      url.searchParams.get("ozon_account") === "visual-ozon-2"
    )
      value = {
        items: [],
        counts: { total: 0, mapped: 0, unmapped: 0, attention: 0 },
        total: 0,
        offset: 0,
        has_next: false,
        ozon_imported: false,
        moy_imported: true,
      };
    await route.fulfill({ json: value });
  });
}
const routes = [
  ["/", "Добро пожаловать в STYK"],
  ["/products", "Товары"],
  ["/products/rules", "Товары"],
  ["/products/import", "Импорт товаров"],
  ["/products/matching", "Автосопоставление товаров"],
  ["/orders", "Заказы"],
  ["/warehouses", "Склады"],
  ["/integrations", "Интеграции"],
  ["/diagnostics", "Диагностика"],
];
for (const width of [1275, 1051, 1050, 390]) {
  test(`orders header controls align before and after visiting settings at ${width}px`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 932 });
    await fixtures(page);
    await page.goto(origin + "/orders");
    const settings = page.getByRole("link", { name: "Настройки", exact: true });
    for (const visit of ["direct", "return"]) {
      await expect(settings).toBeVisible();
      await page.evaluate(() => document.fonts.ready);
      const layout = await page
        .locator(".orders-heading")
        .evaluate((heading) => {
          const settings = heading.querySelector(".orders-settings-link")!;
          const action = heading.querySelector("button")!;
          const box = (element: Element) => {
            const { top, bottom, left, right, height } =
              element.getBoundingClientRect();
            return { top, bottom, left, right, height };
          };
          return {
            settings: box(settings),
            action: box(action),
            title: box(heading.querySelector("h1")!),
            context: box(heading.querySelector(".orders-account-context")!),
            pageWidth: document.documentElement.scrollWidth,
            viewport: innerWidth,
          };
        });
      expect(layout.settings.height).toBeGreaterThanOrEqual(42);
      expect(
        Math.abs(layout.settings.height - layout.action.height),
      ).toBeLessThan(1);
      expect(Math.abs(layout.settings.top - layout.action.top)).toBeLessThan(1);
      expect(layout.settings.right).toBeLessThan(layout.action.left);
      expect(layout.action.right).toBeLessThanOrEqual(width);
      expect(layout.pageWidth).toBeLessThanOrEqual(layout.viewport);
      if (width > 1050) {
        expect(Math.abs(layout.settings.top - layout.title.top)).toBeLessThan(
          1,
        );
      } else {
        expect(layout.settings.top).toBeGreaterThan(layout.context.bottom);
        expect(Math.abs(layout.settings.left - layout.title.left)).toBeLessThan(
          1,
        );
      }
      await settings.focus();
      await expect(settings).toBeFocused();
      await page.screenshot({
        path: `test-results/orders-header/${width}-${visit}.png`,
      });
      if (visit === "direct") {
        await settings.press("Enter");
        await expect(
          page.getByRole("heading", { name: "Настройки заказов" }),
        ).toBeVisible();
        await page
          .getByRole("link", { name: "К заказам", exact: true })
          .click();
      }
    }
  });
}
for (const [width, height] of [
  [1920, 1080],
  [1440, 900],
  [1366, 768],
  [1280, 800],
  [768, 1024],
  [390, 844],
]) {
  test(`all workspace pages at ${width}x${height}`, async ({ page }) => {
    await page.setViewportSize({ width, height });
    await fixtures(page);
    const errors: string[] = [];
    page.on("pageerror", (error) => errors.push(error.message));
    for (const [path, heading] of routes) {
      await page.goto(origin + path);
      await expect(
        page.getByRole("heading", { name: heading, exact: true }),
      ).toBeVisible();
      await page.evaluate(() => document.fonts.ready);
      expect(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= window.innerWidth,
        ),
        JSON.stringify(
          await page.evaluate(() =>
            Array.from(document.querySelectorAll("body *"))
              .filter(
                (element) =>
                  element.getBoundingClientRect().right > innerWidth + 1 &&
                  getComputedStyle(element).position !== "fixed",
              )
              .slice(0, 10)
              .map((element) => ({
                tag: element.tagName,
                class: element.className,
                right: element.getBoundingClientRect().right,
              })),
          ),
        ),
      ).toBe(true);
      await page.screenshot({
        path: `test-results/redesign/${width}-${path.replaceAll("/", "") || "overview"}.png`,
        fullPage: true,
      });
    }
    expect(errors).toEqual([]);
  });
}
test("mobile navigation traps focus, closes with Escape and navigates", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await fixtures(page);
  await page.goto(origin);
  await page.getByRole("button", { name: "Открыть меню" }).click();
  const dialog = page.getByRole("dialog", { name: "Разделы STYK" });
  await expect(dialog).toBeVisible();
  await page.keyboard.press("Shift+Tab");
  expect(
    await dialog.evaluate((element) =>
      element.contains(document.activeElement),
    ),
  ).toBe(true);
  await page.keyboard.press("Escape");
  await expect(dialog).not.toBeVisible();
  await expect(
    page.getByRole("button", { name: "Открыть меню" }),
  ).toBeFocused();
  await page.getByRole("button", { name: "Открыть меню" }).click();
  await dialog.getByRole("link", { name: "Товары", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Товары", exact: true }),
  ).toBeVisible();
  await expect(dialog).not.toBeVisible();
});
test("merchant authentication screens remain usable", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await fixtures(page, false);
  await page.goto(origin);
  await expect(
    page.getByRole("heading", { name: "Войти в STYK" }),
  ).toBeVisible();
  for (const [action, heading] of [
    ["Создать аккаунт", "Создать аккаунт"],
    ["Забыли пароль?", "Восстановить пароль"],
    ["Отправить подтверждение ещё раз", "Подтвердить почту"],
  ]) {
    await page.getByRole("button", { name: action, exact: true }).click();
    await expect(
      page.getByRole("heading", { name: heading, exact: true }),
    ).toBeVisible();
    await expect(page.getByLabel("Электронная почта")).toBeVisible();
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= window.innerWidth,
      ),
    ).toBe(true);
    await page.screenshot({
      path: `test-results/redesign/auth-${heading}.png`,
      fullPage: false,
    });
    await page.getByRole("button", { name: "Вернуться ко входу" }).click();
  }
});

test("filters, pagination, account selection and connection dialog stay accessible", async ({
  page,
}) => {
  await fixtures(page);
  await page.goto(origin + "/products");
  const searchRequest = page.waitForRequest(
    (request) =>
      new URL(request.url()).pathname === "/api/products" &&
      new URL(request.url()).searchParams.get("search") === "STYK-1000",
  );
  await page
    .getByRole("textbox", { name: "Поиск", exact: true })
    .fill("STYK-1000");
  await searchRequest;
  const nextPage = page.waitForRequest(
    (request) =>
      new URL(request.url()).pathname === "/api/products" &&
      new URL(request.url()).searchParams.get("offset") === "20",
  );
  await page
    .getByRole("button", { name: "Следующая страница", exact: true })
    .click();
  await nextPage;
  await expect(
    page.getByRole("combobox", { name: "Ozon", exact: true }),
  ).toHaveValue("visual-ozon");
  await expect(
    page
      .getByRole("combobox", { name: "Ozon", exact: true })
      .getByRole("option"),
  ).toHaveCount(3);
  await page
    .getByRole("combobox", { name: "Ozon", exact: true })
    .selectOption("visual-ozon-2");
  await expect(
    page.getByRole("combobox", { name: "Ozon", exact: true }),
  ).toHaveValue("visual-ozon-2");
  await page
    .getByRole("combobox", { name: "Ozon", exact: true })
    .selectOption("visual-ozon");
  await page
    .locator(".app-tabs")
    .getByRole("link", { name: "Интеграции", exact: true })
    .click();
  await page
    .locator('[data-provider-group="ozon"] .integration-account-card')
    .first()
    .getByRole("button", { name: "Настройки", exact: true })
    .click();
  const dialog = page.getByRole("dialog", {
    name: "Подключение Ozon",
    exact: true,
  });
  await expect(dialog).toBeVisible();
  for (const width of [1440, 390]) {
    await page.setViewportSize({ width, height: 900 });
    await expect(dialog).toBeVisible();
    expect(
      await dialog.evaluate(
        (element) => element.getBoundingClientRect().right <= window.innerWidth,
      ),
    ).toBe(true);
    await page.screenshot({
      path: `test-results/redesign/${width}-connection-dialog.png`,
      fullPage: false,
    });
  }
  await page.keyboard.press("Escape");
  await expect(dialog).not.toBeVisible();
});

for (const width of [1440, 390]) {
  test(`workspace selection and new account flow at ${width}px`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 900 });
    await fixtures(page);
    await page.goto(origin + "/products");
    await expect(
      page.getByRole("heading", { name: "Товары", exact: true }),
    ).toBeVisible();
    await expect(page.locator(".app-topbar")).toHaveCount(0);
    if (width === 390)
      await page.getByRole("button", { name: "Открыть меню" }).click();
    const workspace = page.getByRole("region", {
      name: "Рабочее пространство",
      exact: true,
    });
    await expect(
      workspace.getByRole("combobox", { name: "МойСклад", exact: true }),
    ).toHaveValue("visual-moy");
    await expect(workspace.locator(".app-nav-label")).toHaveCount(0);
    const moyBounds = await workspace
      .locator(".fixed-moy-account")
      .boundingBox();
    const marketBounds = await workspace
      .getByRole("group", { name: "Маркетплейс" })
      .boundingBox();
    expect(moyBounds!.y + moyBounds!.height).toBeLessThan(marketBounds!.y);
    await workspace
      .getByRole("combobox", { name: "Ozon", exact: true })
      .selectOption("visual-ozon-2");
    await workspace
      .getByRole("button", { name: "Wildberries", exact: true })
      .click();
    await expect(
      workspace.getByRole("button", { name: "Добавить аккаунт", exact: true }),
    ).toBeDisabled();
    await expect(
      page.getByRole("heading", { name: "Wildberries", exact: true }),
    ).toBeVisible();
    await expect(
      page.getByRole("heading", { name: "Товары", exact: true }),
    ).toHaveCount(0);
    await workspace
      .getByRole("button", { name: "Яндекс Маркет", exact: true })
      .click();
    await expect(
      page.getByRole("heading", { name: "Яндекс Маркет", exact: true }),
    ).toBeVisible();
    await workspace.getByRole("button", { name: "Ozon", exact: true }).click();
    await expect(
      workspace.getByRole("combobox", { name: "Ozon", exact: true }),
    ).toHaveValue("visual-ozon-2");
    const brand =
      width === 390
        ? page.locator(".navigation-drawer .app-brand")
        : page.locator(".app-sidebar .app-brand");
    expect((await brand.textContent())!.replaceAll(/\s/g, "")).toBe("STYK");
    await workspace
      .getByRole("button", { name: "Добавить аккаунт", exact: true })
      .click();
    const connection = page.getByRole("dialog", {
      name: "Подключение Ozon",
      exact: true,
    });
    await expect(connection).toBeVisible();
    await expect(
      connection.getByLabel("ID клиента Ozon", { exact: true }),
    ).toHaveValue("");
    await expect(
      page.getByRole("dialog", { name: "Разделы STYK" }),
    ).toHaveCount(0);
    await connection
      .getByRole("button", { name: "Закрыть настройки", exact: true })
      .click();
    if (width === 390)
      await page.getByRole("button", { name: "Открыть меню" }).click();
    const profile =
      width === 390
        ? page.locator(".navigation-drawer .user-menu")
        : page.locator(".app-sidebar .user-menu");
    await profile.locator("summary").click();
    await expect(
      profile.getByRole("link", { name: "Настройки подключений", exact: true }),
    ).toBeVisible();
    await page.screenshot({
      path: `test-results/redesign/${width}-workspace-profile.png`,
      fullPage: width !== 390,
    });
    await profile
      .getByRole("link", { name: "Настройки подключений", exact: true })
      .click();
    await expect(
      page.getByRole("heading", { name: "Интеграции", exact: true }),
    ).toBeVisible();
    await expect(
      page.getByRole("dialog", { name: "Разделы STYK" }),
    ).toHaveCount(0);
  });
}

test("hover preserves button labels across all workspace pages", async ({
  page,
}, testInfo) => {
  test.setTimeout(90_000);
  await page.setViewportSize({ width: 1440, height: 1000 });
  await fixtures(page);
  const results: Record<string, unknown> = {};
  for (const [path, heading] of routes) {
    await page.goto(origin + path);
    await expect(
      page.getByRole("heading", { name: heading, exact: true }),
    ).toBeVisible();
    results[path] = await auditButtonHovers(page, heading);
  }
  await testInfo.attach("workspace-hover-contrast", {
    body: JSON.stringify(results, null, 2),
    contentType: "application/json",
  });
});

test("warehouse account change resets search and closes old selection without leaking old rows", async ({
  page,
}) => {
  await fixtures(page);
  await page.goto(origin + "/warehouses");
  await page.getByLabel("Поиск склада").fill("Екатеринбург");
  await page
    .locator(".wh-table tbody tr")
    .filter({ hasText: "Склад Ozon Екатеринбург" })
    .getByRole("button", { name: "Сопоставить", exact: true })
    .click();
  await expect(
    page.getByRole("dialog", { name: "Сопоставить склады", exact: true }),
  ).toBeVisible();
  await page.keyboard.press("Escape");
  await page
    .getByRole("combobox", { name: "Ozon", exact: true })
    .selectOption("visual-ozon-2");
  await expect(page.getByLabel("Поиск склада")).toHaveValue("");
  await expect(page.locator(".wh-table tbody tr")).toHaveCount(0);
  await expect(
    page.getByRole("dialog", { name: "Сопоставить склады", exact: true }),
  ).toHaveCount(0);
  await expect(
    page.getByText("Склад Ozon Екатеринбург", { exact: true }),
  ).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Все (0)" })).toBeVisible();
});

for (const width of [1864, 1543, 1280, 1101, 1100, 1024, 901, 801, 768, 390]) {
  test(`warehouse table contains long store names at ${width}px`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 932 });
    await fixtures(page);
    await page.goto(origin + "/warehouses");
    await expect(page.locator(".wh-table tbody tr")).toHaveCount(6);
    await page.evaluate(() => document.fonts.ready);
    const geometry = await page.locator(".wh-table").evaluate((table) => {
      const surface = table.closest(".wh-surface")!;
      const padding = parseFloat(getComputedStyle(surface).paddingRight);
      return {
        pageWidth: document.documentElement.scrollWidth,
        viewport: innerWidth,
        tableRight: table.getBoundingClientRect().right,
        contentRight: surface.getBoundingClientRect().right - padding,
        overflowingCells: Array.from(table.querySelectorAll("td"))
          .filter((cell) => cell.scrollWidth > cell.clientWidth + 1)
          .map((cell) => cell.textContent),
      };
    });
    expect(geometry.pageWidth).toBeLessThanOrEqual(geometry.viewport);
    expect(geometry.tableRight).toBeLessThanOrEqual(geometry.contentRight + 1);
    expect(geometry.overflowingCells).toEqual([]);
    const mappedStatusHeights = await page
      .locator('.wh-status[data-state="mapped"]')
      .evaluateAll((statuses) =>
        statuses.map((status) => status.getBoundingClientRect().height),
      );
    expect(mappedStatusHeights.every((height) => height <= 32)).toBe(true);
    await page.screenshot({
      path: `test-results/warehouse-comments/table-${width}.png`,
      fullPage: true,
    });
  });
}
for (const width of [1543, 390]) {
  test(`warehouse search has one focus ring and source card spacing at ${width}px`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 932 });
    await fixtures(page);
    await page.goto(origin + "/warehouses");
    await page
      .locator(".wh-table tbody tr")
      .filter({ hasText: "Склад Ozon Екатеринбург" })
      .getByRole("button", { name: "Сопоставить", exact: true })
      .click();
    const input = page.getByRole("textbox", { name: "Найти склад МойСклад" });
    await expect(input).toBeFocused();
    await expect(
      page.getByRole("radio", { name: /Склад Екатеринбург/ }),
    ).toBeVisible();
    const styles = await input.evaluate((element) => {
      const label = element.closest("label")!,
        source = document.querySelector(".wh-source")!,
        subtitle = document.querySelector(".wh-selection > .wh-subtitle")!;
      return {
        outline: getComputedStyle(element).outlineStyle,
        border: getComputedStyle(element).borderWidth,
        shadow: getComputedStyle(element).boxShadow,
        outerOutline: getComputedStyle(label).outlineStyle,
        outerShadow: getComputedStyle(label).boxShadow,
        gap:
          source.getBoundingClientRect().top -
          subtitle.getBoundingClientRect().bottom,
        inputRight: element.getBoundingClientRect().right,
        labelRight: label.getBoundingClientRect().right,
      };
    });
    expect(styles.outline).toBe("none");
    expect(styles.border).toBe("0px");
    expect(styles.shadow).toBe("none");
    expect(styles.outerOutline).toBe("none");
    expect(styles.outerShadow).not.toBe("none");
    expect(styles.gap).toBeGreaterThanOrEqual(16);
    expect(styles.inputRight).toBeLessThan(styles.labelRight);
    await page.screenshot({
      path: `test-results/warehouse-comments/search-${width}.png`,
    });
    await page.keyboard.press("Tab");
    await page.keyboard.press("Shift+Tab");
    await expect(input).toBeFocused();
    expect(
      await input.evaluate((element) => getComputedStyle(element).outlineStyle),
    ).toBe("none");
  });
}

test("confirmation feedback follows warehouse, order and overview content", async ({
  page,
}) => {
  await fixtures(page);
  const id = "a".repeat(32);
  let saved = false;
  const reviewed = {
    review: {
      id,
      tenant: scope.tenant,
      moysklad_account: scope.moysklad_account,
      ozon_account: scope.ozon_account,
      moysklad_warehouse_id: "m-free",
      ozon_warehouse_id: "warehouse-3",
      moysklad_import_id: 1,
      ozon_import_id: 2,
      existing_mapping_id: null,
      existing_mapping_version: null,
      created_at: new Date().toISOString(),
      expires_at: "2099-01-01T00:00:00Z",
    },
    status: "CURRENT",
    principal_type: "MERCHANT_SESSION",
  };
  await page.route("**/api/warehouse-mapping-review?**", (route) =>
    route.fulfill({
      json: {
        status: saved ? "ALREADY_MAPPED" : "ELIGIBLE_FOR_REVIEW",
        moysklad: {
          id: "m-free",
          name: "Склад Екатеринбург",
          account: scope.moysklad_account,
        },
        ozon: {
          id: "warehouse-3",
          name: "Склад Ozon Екатеринбург",
          account: scope.ozon_account,
          is_rfbs: false,
        },
        mappings: [],
        history: [],
        has_next: false,
        offset: 0,
      },
    }),
  );
  await page.route("**/api/warehouse-mapping-reviews**", (route) => {
    if (route.request().url().endsWith("/confirm")) {
      saved = true;
      return route.fulfill({
        json: {
          review_id: id,
          mapping_id: 9,
          mapping_version: 1,
          evidence_class: "OPERATOR_ATTESTED",
          confirmed_at: new Date().toISOString(),
          idempotent_replay: false,
        },
      });
    }
    return route.fulfill({ json: reviewed });
  });
  await page.goto(origin + "/warehouses");
  await page
    .locator(".wh-table tbody tr")
    .filter({ hasText: "Склад Ozon Екатеринбург" })
    .getByRole("button", { name: "Сопоставить", exact: true })
    .click();
  const dialog = page.getByRole("dialog", {
    name: "Сопоставить склады",
    exact: true,
  });
  await dialog.getByRole("radio", { name: /Склад Екатеринбург/ }).check();
  await dialog.getByRole("button", { name: "Проверить соответствие" }).click();
  await expect(
    dialog.getByText("Можно сопоставить", { exact: true }),
  ).toBeVisible();
  await dialog
    .getByRole("checkbox", {
      name: "Подтверждаю, что эти склады соответствуют друг другу",
    })
    .check();
  await dialog
    .getByRole("button", { name: "Подтвердить сопоставление", exact: true })
    .click();
  await expect(
    dialog.getByText("Соответствие складов сохранено.", { exact: true }),
  ).toBeVisible();
  await dialog.getByRole("button", { name: "Готово" }).click();
  for (const [label, selector] of [
    ["Склады", ".wh-page"],
    ["Заказы", ".orders-console"],
    ["Обзор", ".overview-page"],
  ]) {
    if (label !== "Склады")
      await page
        .locator(".app-tabs")
        .getByRole("link", { name: label, exact: true })
        .click();
    const feedback = page.getByRole("region", { name: "Результаты операций" });
    await expect(feedback.getByText(/Соответствие подтверждено/)).toBeVisible();
    expect(
      await page
        .locator(".content")
        .evaluate((element) =>
          element.lastElementChild?.classList.contains("app-feedback"),
        ),
    ).toBe(true);
    const bounds = await page.locator(selector).evaluate((element) => ({
      bottom: element.getBoundingClientRect().bottom,
      feedbackTop: document
        .querySelector(".app-feedback")!
        .getBoundingClientRect().top,
    }));
    expect(bounds.feedbackTop).toBeGreaterThanOrEqual(bounds.bottom);
  }
});

test("removed catalog route stays missing without a redirect", async ({
  page,
}) => {
  await fixtures(page);
  await page.goto(origin + "/catalog-sync");
  await expect(
    page.getByRole("heading", { name: "Страница не найдена" }),
  ).toBeVisible();
  await expect(page).toHaveURL(origin + "/catalog-sync");
  await expect(
    page
      .getByRole("navigation", { name: "Разделы" })
      .getByRole("link", { name: "Каталог", exact: true }),
  ).toHaveCount(0);
});
for (const width of [1440, 768, 390])
  test(`matching preview and explicit result at ${width}px`, async ({
    page,
  }) => {
    await fixtures(page);
    await page.setViewportSize({ width, height: 950 });
    const items = [
      {
        product_id: "101",
        offer_id: "STYK-01",
        name: "Органайзер для рабочего стола",
        status: "exact",
        reason: "exact_sku",
        candidates: [
          {
            kind: "product",
            id: "p1",
            name: "Органайзер для рабочего стола",
            article: "STYK-01",
            evidence: "exact_sku",
          },
        ],
      },
      {
        product_id: "102",
        offer_id: "STYK-02",
        name: "Органайзер большой",
        status: "review",
        reason: "similar_name",
        candidates: [
          {
            kind: "product",
            id: "p2",
            name: "Органайзер для бумаг",
            article: "STYK-03",
            evidence: "similar_name",
          },
        ],
      },
      {
        product_id: "103",
        offer_id: "STYK-04",
        name: "Подставка для карандашей",
        status: "conflict",
        reason: "duplicate_sku",
        candidates: [],
      },
    ];
    let policy = {
      revision: 0,
      exact_sku: true,
      names: true,
      auto_confirm: false,
      after_import: false,
    };
    let runs = 0;
    await page.route("**/api/product-matching/**", async (route) => {
      const request = route.request(),
        path = new URL(request.url()).pathname;
      if (path.endsWith("settings")) {
        if (request.method() === "POST")
          policy = { ...request.postDataJSON().policy, revision: 1 };
        return route.fulfill({ json: policy });
      }
      if (path.includes("/jobs"))
        return route.fulfill({
          json: {
            id: "visual-job",
            state: "COMPLETED",
            processed: items.length,
            total: items.length,
            pages: 1,
            code: "",
            preview: {
              id: "visual-preview",
              scope,
              policy,
              items,
              expires_at: "2099-01-01T00:00:00Z",
            },
          },
        });
      if (path.endsWith("preview"))
        return route.fulfill({
          json: {
            id: "visual-preview",
            scope,
            policy,
            items,
            expires_at: "2099-01-01T00:00:00Z",
          },
        });
      runs++;
      expect(request.postDataJSON().product_ids).toEqual(["101"]);
      return route.fulfill({
        json: {
          id: "visual-run",
          preview_id: "visual-preview",
          confirmed: 1,
          items: [
            { ...items[0], status: "confirmed", reason: "confirmed_by_rule" },
          ],
        },
      });
    });
    await page.goto(origin + "/products/matching");
    await page
      .getByRole("radio", { name: /Разрешить подтверждение уникальных/ })
      .check();
    await page
      .getByRole("button", { name: "Сохранить правила", exact: true })
      .click();
    await expect(
      page.getByRole("button", { name: "Сохранить правила", exact: true }),
    ).toBeDisabled();
    await page
      .getByRole("button", {
        name: "Посмотреть предварительный результат",
      })
      .click();
    await expect(
      page.getByLabel("Выбрать пару STYK-02", { exact: true }),
    ).toBeDisabled();
    await expect(
      page.getByLabel("Выбрать пару STYK-04", { exact: true }),
    ).toBeDisabled();
    expect(runs).toBe(0);
    await page.screenshot({
      path: `test-results/redesign/${width}-matching-preview.png`,
      fullPage: true,
    });
    await page.getByLabel("Выбрать пару STYK-01", { exact: true }).check();
    await page.getByLabel(/Разрешаю подтвердить выбранные точные пары/).check();
    await page
      .getByRole("button", { name: "Подтвердить выбранные пары" })
      .click();
    await expect(
      page.getByRole("heading", { name: "Результаты сопоставления" }),
    ).toBeVisible();
    expect(runs).toBe(1);
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth + 1,
      ),
    ).toBe(true);
    await page.screenshot({
      path: `test-results/redesign/${width}-matching-result.png`,
      fullPage: true,
    });
  });

for (const width of [768, 390]) {
  test(`expanded integration setup fits ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 });
    await fixtures(page);
    await page.goto(origin + "/integrations");
    await page
      .getByRole("button", { name: "Настройка синхронизации", exact: true })
      .click();
    const steps = page.locator(".integration-console .setup-steps");
    await expect(steps).toBeVisible();
    const columns = await steps.evaluate(
      (element) =>
        getComputedStyle(element).gridTemplateColumns.split(" ").length,
    );
    expect(columns).toBe(width === 390 ? 1 : 3);
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBe(true);
    await page.screenshot({
      path: `test-results/redesign/${width}-integration-setup.png`,
      fullPage: true,
    });
  });
}
