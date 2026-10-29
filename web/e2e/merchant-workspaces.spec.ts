import { expect, test, type Page } from "@playwright/test";
import { mkdirSync } from "node:fs";
import { join } from "node:path";

const origin = process.env.STYK_MERCHANT_ORIGIN || "";
const testAPI = process.env.STYK_MERCHANT_TEST_API || "";
const testSecret = process.env.STYK_MERCHANT_TEST_SECRET || "";
const aEmail = "browser-a@example.test";
const bEmail = "browser-b@example.test";
const password = "synthetic-merchant-password";
const aTenant = "cross-e2e";
const aMoy = "2928cf35-d30a-11f1-0a80-1b9000000355";
const aOzon = "1549477";
const bMoy = "00000000-0000-4000-8000-000000000002";
const bOzon = "987654321";

test("two merchants register, import independently and switch only through membership", async ({
  browser,
}) => {
  test.skip(
    !origin || !testAPI || !testSecret,
    "requires isolated PostgreSQL and loopback providers",
  );
  test.setTimeout(120_000);
  const aContext = await browser.newContext({
    viewport: { width: 1440, height: 900 },
  });
  const bContext = await browser.newContext({
    viewport: { width: 1440, height: 900 },
  });
  const a = await aContext.newPage();
  const b = await bContext.newPage();
  for (const page of [a, b]) {
    page.setDefaultTimeout(10_000);
    page.setDefaultNavigationTimeout(10_000);
    await page.route("**/*", (route) =>
      ["127.0.0.1", "localhost"].includes(
        new URL(route.request().url()).hostname,
      )
        ? route.continue()
        : route.abort(),
    );
  }
  await register(a, aEmail, "Магазин A");
  await register(b, bEmail, "Магазин B");
  const claim = await a.request.post(`${testAPI}/__test/claim`, {
    headers: { "X-Test-Secret": testSecret },
    data: { email: aEmail },
  });
  expect(claim.status()).toBe(204);
  await a.reload();
  await selectWorkspace(a, aTenant);
  await selectAccounts(a, aMoy, aOzon);
  await a.getByRole("link", { name: "Товары", exact: true }).click();
  await expect(a.getByText("ATEST-897677", { exact: true })).toBeVisible();

  await b.getByRole("link", { name: "Интеграции", exact: true }).click();
  await connect(b, "МойСклад", "Склад B", "synthetic-moy-b");
  await connect(b, "Ozon", "Магазин Ozon B", "synthetic-ozon-b", bOzon);
  await selectAccounts(b, bMoy, bOzon);
  await importCatalog(b, "МойСклад");
  await importCatalog(b, "Ozon");
  await expect(
    b.getByText(/Публикация остатков ожидает отдельной активации/),
  ).toBeVisible();
  await b.getByRole("link", { name: "Товары", exact: true }).click();
  await expect(b.getByText("B-TEST-OFFER", { exact: true })).toBeVisible();
  await expect(b.getByText("ATEST-897677", { exact: true })).toHaveCount(0);
  await b.getByRole("button", { name: /^Сопоставить / }).click();
  await b.getByLabel("Поиск товара МойСклад").fill("Тестовый товар");
  await b.getByRole("button", { name: "Тестовый товар, товар" }).click();
  await b.getByRole("button", { name: "Проверить соответствие" }).click();
  await b.getByLabel("Подтверждаю, что это один и тот же товар").check();
  await b.getByRole("button", { name: "Подтвердить соответствие" }).click();
  await expect(b.getByText(/Соответствие.*сохранено/)).toBeVisible();
  await b.getByRole("button", { name: "Закрыть сопоставление" }).click();
  await b.getByRole("link", { name: "Склады", exact: true }).click();
  for (const warehouse of ["Тестовый склад", "Тестовый FBS"]) {
    await b
      .locator(".warehouse-row")
      .filter({ hasText: warehouse })
      .getByRole("button", { name: "Выбрать для сопоставления" })
      .click();
  }
  const warehouseDialog = b.getByRole("dialog", { name: "Сопоставить склады" });
  await warehouseDialog
    .getByRole("button", { name: "Проверить пару складов" })
    .click();
  await warehouseDialog
    .getByLabel("Подтверждаю соответствие выбранных складов")
    .check();
  await expect(
    warehouseDialog.getByRole("button", {
      name: "Подтвердить соответствие складов",
    }),
    await warehouseDialog.innerText(),
  ).toBeEnabled({ timeout: 10_000 });
  await warehouseDialog
    .getByRole("button", { name: "Подтвердить соответствие складов" })
    .click();
  await expect(
    warehouseDialog.getByText("Соответствие складов сохранено."),
  ).toBeVisible();
  await warehouseDialog
    .getByRole("button", { name: "Закрыть сопоставление складов" })
    .click();
  await b.getByRole("link", { name: "Интеграции", exact: true }).click();
  const automatic = b.getByRole("region", {
    name: "Автоматическая синхронизация",
  });
  await automatic
    .getByRole("button", { name: "Настроить автоматическую синхронизацию" })
    .click();
  const settings = b.getByRole("dialog", {
    name: "Настроить автоматическую синхронизацию?",
  });
  await settings.getByLabel(/Подтверждаю выбранные настройки/).check();
  await settings.getByRole("button", { name: "Сохранить настройки" }).click();
  await expect(settings).toBeHidden();
  await expect(automatic.getByText("Ожидает активации")).toBeVisible();

  const bSession = await (await b.request.get(`${origin}/api/session`)).json();
  const bTenant = bSession.workspaces[0].id as string;
  const draft = await (
    await b.request.get(
      `${origin}/api/stock-sync/automatic?tenant=${encodeURIComponent(bTenant)}&moysklad_account=${bMoy}&ozon_account=${bOzon}`,
    )
  ).json();
  expect(draft.publication_available).toBe(false);
  expect(draft.policy.enabled).toBe(false);
  expect(draft.policy.targets).toHaveLength(1);
  const unauthorizedEnable = await b.request.post(
    `${origin}/api/stock-sync/automatic`,
    {
      headers: { Origin: origin, "X-STYK-CSRF": bSession.csrf },
      data: {
        tenant: bTenant,
        moysklad_account: bMoy,
        ozon_account: bOzon,
        enabled: true,
        acknowledge: true,
        idempotency_key: "browser-enable-not-authorized",
        interval_seconds: 60,
        valid_minutes: 60,
        ozon_product_id: "202",
        ozon_warehouse_id: "1020001323428000",
        safety_stock: "0",
        unreflected: "0",
        unseen_bound: "0",
        maximum: 2,
      },
    },
  );
  expect(unauthorizedEnable.status()).toBeGreaterThanOrEqual(400);
  const shots =
    process.env.STYK_MERCHANT_SCREENSHOTS_DIR ||
    "/tmp/styk-merchant-workspaces";
  mkdirSync(shots, { recursive: true });
  await b.screenshot({
    path: join(shots, "configured-desktop.png"),
    fullPage: true,
  });
  await b.setViewportSize({ width: 390, height: 844 });
  await b.screenshot({
    path: join(shots, "configured-mobile.png"),
    fullPage: true,
  });
  await b.setViewportSize({ width: 1440, height: 900 });
  expect(
    (
      await a.request.get(
        `${origin}/api/registrations?tenant=${encodeURIComponent(bTenant)}`,
      )
    ).status(),
  ).toBe(404);
  expect(
    (
      await b.request.get(`${origin}/api/registrations?tenant=${aTenant}`)
    ).status(),
  ).toBe(404);
  await grantViewer(a, bEmail);
  await b.reload();
  await selectWorkspace(b, aTenant);
  await selectAccounts(b, aMoy, aOzon);
  await b.getByRole("link", { name: "Товары", exact: true }).click();
  await expect(b.getByText("ATEST-897677", { exact: true })).toBeVisible();
  await expect(b.getByText("B-TEST-OFFER", { exact: true })).toHaveCount(0);
  await revokeViewer(a, bEmail);
  expect(
    (
      await b.request.get(`${origin}/api/registrations?tenant=${aTenant}`)
    ).status(),
  ).toBe(404);
  await b.reload();
  await expect(b.getByText("ATEST-897677", { exact: true })).toHaveCount(0);

  await b.screenshot({ path: join(shots, "desktop.png"), fullPage: true });
  await b.setViewportSize({ width: 390, height: 844 });
  await b.screenshot({ path: join(shots, "mobile.png"), fullPage: true });
  expect(
    await b.evaluate(() => document.documentElement.scrollWidth <= innerWidth),
  ).toBe(true);
  await aContext.close();
  await bContext.close();
});

async function register(page: Page, email: string, name: string) {
  await page.goto(origin + "/");
  await page.getByRole("button", { name: "Создать аккаунт" }).click();
  await page.getByLabel("Электронная почта").fill(email);
  await page.getByLabel("Пароль").fill(password);
  await page.getByLabel("Название магазина").fill(name);
  await page.getByRole("button", { name: "Создать аккаунт" }).click();
  await expect(page.getByText(/Если адрес подходит/)).toBeVisible();
  const result = await page.request.get(
    `${testAPI}/__test/mail?email=${encodeURIComponent(email)}`,
    { headers: { "X-Test-Secret": testSecret } },
  );
  const { token } = await result.json();
  expect(token).toBeTruthy();
  await page.goto(`${origin}/#verify=${token}`);
  await page.reload();
  await page.getByLabel("Новый пароль").fill(password);
  await page.getByRole("button", { name: "Подтвердить адрес" }).click();
  await expect(page.getByText(/Почта подтверждена/)).toBeVisible();
  await page.getByLabel("Электронная почта").fill(email);
  await page.getByLabel("Пароль").fill(password);
  await page.getByRole("button", { name: "Войти", exact: true }).click();
  await expect(
    page.getByRole("link", { name: "Интеграции", exact: true }),
  ).toBeVisible();
  expect(await page.evaluate(() => document.cookie)).not.toContain(
    "styk_session",
  );
}

async function selectWorkspace(page: Page, id: string) {
  const selector = page.locator(".workspace-selector");
  if ((await selector.getAttribute("open")) === null)
    await selector.locator("summary").first().click();
  await selector
    .getByRole("combobox", { name: "Рабочее пространство", exact: true })
    .selectOption(id, { timeout: 10_000 });
  await expect(page.locator(".workspace-selector > summary")).toContainText(
    id === aTenant ? aTenant : "Магазин B",
    { timeout: 10_000 },
  );
}

async function selectAccounts(page: Page, moy: string, ozon: string) {
  const selector = page.locator(".workspace-selector");
  if ((await selector.getAttribute("open")) === null)
    await selector.locator("summary").first().click();
  await selector
    .getByRole("combobox", { name: "МойСклад", exact: true })
    .selectOption(moy);
  await selector
    .getByRole("combobox", { name: "Ozon", exact: true })
    .selectOption(ozon);
  await selector.locator("summary").first().click();
}

async function connect(
  page: Page,
  provider: string,
  name: string,
  secret: string,
  account?: string,
) {
  await page
    .getByRole("article", { name: provider })
    .getByRole("button", { name: "Подключить" })
    .click();
  const dialog = page.getByRole("dialog", { name: `Подключение ${provider}` });
  await dialog.getByLabel("Название подключения").fill(name);
  if (account) await dialog.getByLabel("ID клиента Ozon").fill(account);
  await dialog.getByLabel("Ключ доступа", { exact: true }).fill(secret);
  await dialog.getByLabel(/Подтверждаю, что этот аккаунт/).check();
  await dialog.getByRole("button", { name: "Сохранить и проверить" }).click();
  await expect(
    dialog.getByText(/Ключ сохранён, подключение проверено/),
  ).toBeVisible();
  await dialog.getByRole("button", { name: "Закрыть настройки" }).click();
}

async function importCatalog(page: Page, provider: string) {
  const panel = page.getByRole("region", {
    name: `${provider}: импорт данных`,
  });
  await panel
    .getByRole("button", { name: /^(Загрузить|Обновить) данные$/ })
    .click();
  const dialog = page.getByRole("dialog", {
    name: `Загрузить данные ${provider}?`,
  });
  await dialog.getByRole("checkbox").check();
  await dialog.getByRole("button", { name: "Подтвердить загрузку" }).click();
  await expect(
    panel.getByRole("button", { name: "Обновить данные" }),
  ).toBeEnabled({ timeout: 20000 });
}

async function grantViewer(page: Page, email: string) {
  const selector = page.locator(".workspace-selector");
  if ((await selector.getAttribute("open")) === null)
    await selector.locator("summary").first().click();
  await selector.getByText("Настройки пространства").click();
  await selector.getByLabel("Почта участника").fill(email);
  await selector.getByLabel("Роль").selectOption("VIEWER");
  await selector.getByRole("button", { name: "Сохранить доступ" }).click();
  await expect(selector.getByText("Доступ обновлён.")).toBeVisible();
  await selector.locator("summary").first().click();
}

async function revokeViewer(page: Page, email: string) {
  const selector = page.locator(".workspace-selector");
  if ((await selector.getAttribute("open")) === null)
    await selector.locator("summary").first().click();
  const settings = selector.locator(".merchant-workspace-settings");
  if ((await settings.getAttribute("open")) === null)
    await settings.locator("summary").click();
  await settings
    .locator(".merchant-member")
    .filter({ hasText: email })
    .getByRole("button", { name: "Отозвать" })
    .click();
  await expect(settings.getByText("Доступ отозван.")).toBeVisible();
}
