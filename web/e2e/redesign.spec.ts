import { test, expect, type Page } from "@playwright/test";

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
          match_status: "CONFIRMED",
          candidate_count: 0,
          candidates: [],
          candidates_truncated: false,
        })),
        has_next: true,
        offset: 0,
      };
    else if (path === "/api/moysklad-products")
      value = { items: [], has_next: false, offset: 0 };
    else if (path === "/api/warehouses")
      value = {
        items: [
          {
            id: `warehouse-${url.searchParams.get("source")}`,
            name: "Основной склад · Москва",
            stock_rows: 128,
            mapped: true,
            is_rfbs: false,
          },
        ],
        has_next: false,
        offset: 0,
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
    else if (path === "/api/catalog-sync/products")
      value = { products: [], next_cursor: "" };
    else if (path === "/api/catalog-sync/operations")
      value = { operations: [], next_cursor: "" };
    else if (path === "/api/imports")
      value = { items: [], has_next: false, offset: 0 };
    else {
      await route.fulfill({
        status: 404,
        json: { error: "UNAVAILABLE_IN_VISUAL_FIXTURE" },
      });
      return;
    }
    await route.fulfill({ json: value });
  });
}
const routes = [
  ["/", "Обзор"],
  ["/products", "Сопоставление товаров"],
  ["/catalog-sync", "Синхронизация каталога"],
  ["/warehouses", "Сопоставление складов"],
  ["/integrations", "Интеграции"],
  ["/diagnostics", "Диагностика"],
];
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
    page.getByRole("heading", { name: "Сопоставление товаров" }),
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
      new URL(request.url()).searchParams.get("offset") === "50",
  );
  await page.getByRole("button", { name: "Далее", exact: true }).click();
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
    .getByRole("article", { name: "Ozon", exact: true })
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
      page.getByRole("heading", { name: "Сопоставление товаров" }),
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
    ).toHaveCount(0);
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
    await workspace.getByRole("button", { name: "WB", exact: true }).click();
    await expect(
      workspace.getByRole("button", { name: "Добавить аккаунт", exact: true }),
    ).toBeDisabled();
    await expect(
      page.getByRole("heading", { name: "Wildberries", exact: true }),
    ).toBeVisible();
    await expect(
      page.getByRole("heading", { name: "Сопоставление товаров" }),
    ).toHaveCount(0);
    await workspace.getByRole("button", { name: "ЯМ", exact: true }).click();
    await expect(
      page.getByRole("heading", { name: "Яндекс Маркет", exact: true }),
    ).toBeVisible();
    await workspace.getByRole("button", { name: "OZON", exact: true }).click();
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
