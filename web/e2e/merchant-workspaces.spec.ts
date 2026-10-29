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
const simulatedWorkspace = "synthetic-auto-browser-b";
const simulation = process.env.STYK_MERCHANT_AUTOSIM === "1";

test("two merchants register, use independent accounts and stay isolated", async ({
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
  await register(a, aEmail, !simulation);
  await register(b, bEmail);
  const claim = await a.request.post(`${testAPI}/__test/claim`, {
    headers: { "X-Test-Secret": testSecret },
    data: { email: aEmail },
  });
  expect(claim.status()).toBe(204);
  if (simulation) {
    const claimB = await b.request.post(`${testAPI}/__test/claim`, {
      headers: { "X-Test-Secret": testSecret },
      data: { email: bEmail, workspace: simulatedWorkspace },
    });
    expect(claimB.status()).toBe(204);
    await b.reload();
    await login(b, bEmail);
  }
  await a.reload();
  await login(a, aEmail);
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
  await expect(b.getByText("Товар Ozon для проверки", { exact: true })).toBeVisible();
  await expect(b.getByText("Offer ID: B-TEST-OFFER", { exact: true })).toBeVisible();
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
  await settings
    .getByRole("button", {
      name: simulation
        ? "Включить автоматическую проверку"
        : "Сохранить настройки",
    })
    .click();
  await expect(settings).toBeHidden();
  if (!simulation)
    await expect(automatic.getByText("Ожидает активации")).toBeVisible();

  const bSession = await (await b.request.get(`${origin}/api/session`)).json();
  const bTenant = simulation
    ? simulatedWorkspace
    : (bSession.account_id as string);
  const autoURL = `${origin}/api/stock-sync/automatic?tenant=${encodeURIComponent(bTenant)}&moysklad_account=${bMoy}&ozon_account=${bOzon}`;
  if (simulation) {
    await expect
      .poll(
        async () =>
          (await (await b.request.get(autoURL)).json()).last_cycle?.outcome,
        { timeout: 45_000, intervals: [500, 1000, 2000] },
      )
      .toBe("RECONCILED");
    const reconciled = await (await b.request.get(autoURL)).json();
    expect(reconciled.last_cycle.publication_id).toMatch(/^[a-f0-9]{32}$/);
    expect(reconciled.last_cycle.metrics.external_writes).toBe(1);
    expect(reconciled.last_cycle.metrics.reconciled_operations).toBe(1);
    expect(reconciled.candidates[0]).toMatchObject({
      ozon_product_id: "202",
      ozon_warehouse_id: "1020001323428000",
      source_physical: "2",
      source_reserve: "0",
      last_state: "RECONCILED",
      readback_quantity: "2",
    });
    await expect(
      automatic.getByText("Последняя отправка сверена с данными Ozon."),
    ).toBeVisible();
  } else {
    const draft = await (await b.request.get(autoURL)).json();
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
  }
  const shots =
    process.env.STYK_MERCHANT_SCREENSHOTS_DIR ||
    "/tmp/styk-merchant-workspaces";
  mkdirSync(shots, { recursive: true });
  await b.screenshot({
    path: join(
      shots,
      simulation ? "simulator-desktop.png" : "configured-desktop.png",
    ),
    fullPage: true,
  });
  await b.setViewportSize({ width: 390, height: 844 });
  await b.screenshot({
    path: join(
      shots,
      simulation ? "simulator-mobile.png" : "configured-mobile.png",
    ),
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
  expect((await a.request.get(`${origin}/api/workspaces`)).status()).toBe(404);
  expect(await b.getByText(/Рабочее пространство/).count()).toBe(0);
  await expect(b.getByText("ATEST-897677", { exact: true })).toHaveCount(0);

  await b.screenshot({ path: join(shots, "desktop.png"), fullPage: true });
  await b.setViewportSize({ width: 390, height: 844 });
  await b.screenshot({ path: join(shots, "mobile.png"), fullPage: true });
  expect(
    await b.evaluate(() => document.documentElement.scrollWidth <= innerWidth),
  ).toBe(true);
  await checkPasswordReset(b, browser, bEmail);
  await aContext.close();
  await bContext.close();
});

async function register(page: Page, email: string, slow = false) {
  await page.goto(origin + "/");
  await page.getByRole("button", { name: "Создать аккаунт" }).click();
  await page.getByLabel("Электронная почта").fill(email);
  await expect(page.getByLabel("Название магазина")).toHaveCount(0);
  if (slow) {
    await page.waitForTimeout(16_000);
    await expect(page.getByLabel("Электронная почта")).toHaveValue(email);
    await page.evaluate(() => window.dispatchEvent(new Event("focus")));
    await expect(
      page.getByRole("heading", { name: "Создать аккаунт" }),
    ).toBeVisible();
  }
  await page.getByRole("button", { name: "Создать аккаунт" }).click();
  await expect(page.getByText(/Если адрес доступен/)).toBeVisible();
  const result = await page.request.get(
    `${testAPI}/__test/mail?email=${encodeURIComponent(email)}`,
    { headers: { "X-Test-Secret": testSecret } },
  );
  const { token } = await result.json();
  expect(token).toBeTruthy();
  await page.goto(`${origin}/#verify=${token}`);
  await page.getByLabel("Новый пароль").fill(password);
  if (slow) {
    await page.waitForTimeout(16_000);
    await expect(page.getByLabel("Новый пароль")).toHaveValue(password);
    expect(await page.evaluate(() => location.hash)).toBe("");
  }
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

async function checkPasswordReset(
  page: Page,
  browser: import("@playwright/test").Browser,
  email: string,
) {
  const otherSession = await browser.newContext();
  try {
    const login = await otherSession.request.post(`${origin}/api/session`, {
      headers: { Origin: origin },
      data: { email, password },
    });
    expect(login.status()).toBe(204);
    expect(
      (await otherSession.request.get(`${origin}/api/session`)).status(),
    ).toBe(200);
    const otherPage = await otherSession.newPage();
    await otherPage.goto(origin + "/");
    await expect(
      otherPage.getByRole("link", { name: "Интеграции", exact: true }),
    ).toBeVisible();
    const selector = page.locator(".workspace-selector");
    if ((await selector.getAttribute("open")) === null)
      await selector.locator("summary").first().click();
    await selector.getByRole("button", { name: "Выйти" }).click();
    await page.getByRole("button", { name: "Забыли пароль?" }).click();
    await page.getByLabel("Электронная почта").fill(email);
    await page.getByRole("button", { name: "Отправить письмо" }).click();
    await expect(page.getByText(/Если адрес подходит/)).toBeVisible();
    const result = await page.request.get(`${testAPI}/__test/mail`, {
      headers: { "X-Test-Secret": testSecret },
      params: { email, purpose: "RESET_PASSWORD" },
    });
    const { token } = await result.json();
    expect(token).toBeTruthy();
    await otherPage.goto(`${origin}/#reset=${token}`);
    await otherPage
      .getByLabel("Новый пароль")
      .fill("replacement-test-password");
    await otherPage.getByRole("button", { name: "Сохранить пароль" }).click();
    await expect(otherPage.getByText(/Пароль изменён/)).toBeVisible();
    expect(
      (await otherSession.request.get(`${origin}/api/session`)).status(),
    ).toBe(401);
    await otherPage
      .getByRole("button", { name: "Вернуться в приложение" })
      .click();
    await expect(
      otherPage.getByRole("heading", { name: "Войти в STYK" }),
    ).toBeVisible();
    await page.getByRole("button", { name: "Вернуться ко входу" }).click();
    await page.getByLabel("Электронная почта").fill(email);
    await page.getByLabel("Пароль").fill(password);
    await page.getByRole("button", { name: "Войти", exact: true }).click();
    await expect(page.getByRole("alert")).toContainText("Не удалось войти");
    await page.getByLabel("Пароль").fill("replacement-test-password");
    await page.getByRole("button", { name: "Войти", exact: true }).click();
    await expect(
      page.getByRole("link", { name: "Интеграции", exact: true }),
    ).toBeVisible();
  } finally {
    await otherSession.close();
  }
}

async function login(page: Page, email: string) {
  await page.getByLabel("Электронная почта").fill(email);
  await page.getByLabel("Пароль").fill(password);
  await page.getByRole("button", { name: "Войти", exact: true }).click();
  await expect(page.getByRole("link", { name: "Интеграции", exact: true })).toBeVisible();
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
