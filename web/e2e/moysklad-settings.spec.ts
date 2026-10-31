import {
  expect,
  test,
  type APIRequestContext,
  type BrowserContext,
} from "@playwright/test";

const origin = process.env.STYK_SETTINGS_ORIGIN || "";
const testAPI = process.env.STYK_SETTINGS_TEST_API || "";
const password = "synthetic-settings-password";
const account = process.env.STYK_SETTINGS_TEST_ACCOUNT || "";

async function register(request: APIRequestContext, email: string) {
  const headers = { Origin: origin };
  expect(
    (
      await request.post(origin + "/api/registration", {
        headers,
        data: { email },
      })
    ).status(),
  ).toBe(202);
  const mail = await request.get(
    `${testAPI}/__test/mail?email=${encodeURIComponent(email)}`,
    { headers: { "X-Test-Secret": "synthetic-browser-harness" } },
  );
  const { token } = await mail.json();
  expect(
    (
      await request.post(origin + "/api/verification", {
        headers,
        data: { token, password },
      })
    ).status(),
  ).toBe(204);
  expect(
    (
      await request.post(origin + "/api/session", {
        headers,
        data: { email, password },
      })
    ).status(),
  ).toBe(204);
  return (await (await request.get(origin + "/api/session")).json()) as {
    csrf: string;
    account_id: string;
  };
}
async function assertForeignIsolation(context: BrowserContext, tenant: string) {
  const session = await register(
    context.request,
    `foreign-${Date.now()}@example.test`,
  );
  const target = `${origin}/api/integrations/moysklad/settings`;
  expect(
    (await context.request.get(`${target}?account=${account}`)).status(),
  ).toBe(404);
  const data = { account, name: "Foreign edit", description: "", revision: 1 };
  expect(
    (
      await context.request.post(target, {
        headers: { Origin: origin, "X-STYK-CSRF": session.csrf },
        data,
      })
    ).status(),
  ).toBe(404);
  expect(
    (
      await context.request.post(target, {
        headers: { Origin: origin, "X-STYK-CSRF": session.csrf },
        data: { ...data, tenant },
      })
    ).status(),
  ).toBe(404);
}
test("MoySklad settings persist, protect credentials and work across shell routes", async ({
  page,
  context,
  browser,
}) => {
  test.skip(
    !origin || !testAPI,
    "requires isolated settings Go/PostgreSQL harness",
  );
  test.setTimeout(120_000);
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  const session = await register(
    context.request,
    `settings-${Date.now()}@example.test`,
  );
  const headers = { Origin: origin, "X-STYK-CSRF": session.csrf };
  const save = await context.request.post(
    origin + "/api/integrations/moysklad/credentials",
    {
      headers,
      data: {
        account: "",
        name: "Основной аккаунт",
        secret: "synthetic-settings-initial",
        request_id: crypto.randomUUID(),
        expected_revision: 0,
        acknowledge: true,
      },
    },
  );
  expect(save.status()).toBe(200);
  expect(await save.text()).not.toContain("synthetic-settings-initial");
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto(origin);
  const gear = page.getByRole("link", { name: "Настройки аккаунта МойСклад" });
  await expect(gear).toBeVisible();
  await gear.click();
  await expect(
    page.getByRole("heading", { name: "Настройки аккаунта МойСклад" }),
  ).toBeVisible();
  const name = page.getByLabel("Название аккаунта");
  await expect(name).toHaveValue("Основной аккаунт");
  await expect(
    page.getByRole("button", { name: "Сохранить изменения" }),
  ).toBeDisabled();
  await name.fill("Главный склад STYK");
  await page
    .getByLabel("Описание", { exact: false })
    .fill("Для розничных заказов");
  await page.getByRole("button", { name: "Подключение", exact: true }).click();
  await page
    .getByRole("button", { name: "Основная информация", exact: true })
    .click();
  await expect(name).toHaveValue("Главный склад STYK");
  await page.getByRole("button", { name: "Сохранить изменения" }).click();
  await expect(
    page.getByText("Изменения сохранены", { exact: true }),
  ).toBeVisible();
  await expect(page.locator(".fixed-moy-account strong")).toHaveText(
    "Главный склад STYK",
  );
  await page.reload();
  await expect(name).toHaveValue("Главный склад STYK");
  await expect(page.getByLabel("Описание", { exact: false })).toHaveValue(
    "Для розничных заказов",
  );
  for (const [width, height] of [
    [1920, 1080],
    [1536, 1024],
    [1440, 900],
    [1366, 768],
    [390, 844],
  ]) {
    await page.setViewportSize({ width, height });
    await expect(name).toBeVisible();
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= window.innerWidth,
      ),
    ).toBe(true);
    await page.screenshot({
      path: `../.local/moysklad-settings/general-${width}.png`,
      fullPage: true,
    });
  }
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.getByRole("button", { name: "Подключение", exact: true }).click();
  await page
    .getByRole("button", { name: "Проверить подключение", exact: true })
    .click();
  await expect(
    page.getByText("Подключение проверено. Доступ к МойСклад подтверждён."),
  ).toBeVisible();
  await page.getByRole("button", { name: "Заменить токен" }).click();
  await page.getByLabel("Новый API-токен").fill("invalid-synthetic-token");
  await page.getByRole("checkbox", { name: /аккаунт принадлежит мне/ }).check();
  await page
    .getByRole("button", { name: "Сохранить и проверить токен" })
    .click();
  await expect(
    page.getByText("Сервис не принял ключ. Проверьте его или создайте новый."),
  ).toBeVisible();
  await page.getByLabel("Новый API-токен").fill("synthetic-settings-replaced");
  await page.getByRole("checkbox", { name: /аккаунт принадлежит мне/ }).check();
  await page
    .getByRole("button", { name: "Сохранить и проверить токен" })
    .click();
  await expect(
    page.getByText("Ключ сохранён, подключение проверено."),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Заменить токен" }),
  ).toBeVisible();
  const stored = await context.request.get(
    `${origin}/api/integrations/moysklad/credentials?account=${account}`,
  );
  expect(await stored.text()).not.toContain("synthetic-settings-");
  expect(
    await page.evaluate(
      () => JSON.stringify(localStorage) + JSON.stringify(sessionStorage),
    ),
  ).not.toContain("synthetic-settings-");
  await page.screenshot({
    path: "../.local/moysklad-settings/connection-1440.png",
    fullPage: true,
  });
  await page
    .getByRole("button", { name: "Отключить аккаунт", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Подтвердить отключение" }),
  ).toBeDisabled();
  await page
    .getByRole("checkbox", { name: "Подтверждаю отключение аккаунта" })
    .check();
  await page.getByRole("button", { name: "Подтвердить отключение" }).click();
  await expect(page.locator(".moy-facts .moy-status")).toHaveText("Отключён");
  const profile = await context.request.get(
    `${origin}/api/integrations/moysklad/settings?account=${account}`,
  );
  expect((await profile.json()).name).toBe("Главный склад STYK");
  for (const label of [
    "Синхронизация",
    "Склады и остатки",
    "Сопоставление товаров",
    "Журнал загрузок",
  ])
    await page.getByRole("button", { name: label, exact: true }).click();
  const foreign = await browser.newContext();
  await assertForeignIsolation(foreign, session.account_id);
  await foreign.close();
  expect(
    (
      await context.request.post(
        origin + "/api/integrations/moysklad/settings",
        {
          headers: { Origin: origin },
          data: { account, name: "CSRF edit", description: "", revision: 1 },
        },
      )
    ).status(),
  ).toBe(403);
  for (const route of ["/", "/products", "/warehouses", "/integrations"]) {
    await page.goto(origin + route);
    await gear.click();
    await expect(name).toHaveValue("Главный склад STYK");
  }
  await page.goto(origin);
  await page.getByRole("button", { name: "WB", exact: true }).click();
  await gear.click();
  await expect(name).toHaveValue("Главный склад STYK");
  expect(errors).toEqual([]);
});
