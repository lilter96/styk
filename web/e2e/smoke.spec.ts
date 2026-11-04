import { test, expect } from "@playwright/test";

const origin = process.env.STYK_ORDERS_STACK_ORIGIN || "";
const secret = process.env.STYK_ORDERS_MAIL_SECRET || "";
const email = "orders-browser-register@example.test";
const password = "synthetic-browser-merchant-password";

test("merchant registers, verifies email and retains isolated sessions across API replicas", async ({
  page,
}) => {
  test.setTimeout(60_000);
  expect(
    origin,
    "Use npm run test:browser with a dedicated TEST_DATABASE_URL",
  ).not.toBe("");
  expect(
    secret,
    "The isolated process harness must provide its synthetic mailbox",
  ).not.toBe("");
  expect(new URL(origin).hostname).toBe("127.0.0.1");
  await page.route("**/*", (route) =>
    new URL(route.request().url()).hostname === "127.0.0.1"
      ? route.continue()
      : route.abort(),
  );
  await page.goto(origin);
  await page.getByRole("button", { name: "Создать аккаунт" }).click();
  await page.getByLabel("Электронная почта", { exact: true }).fill(email);
  // The original regression redirected the form on the 15-second session check.
  await page.waitForTimeout(16_000);
  await page.evaluate(() => window.dispatchEvent(new Event("focus")));
  await expect(
    page.getByLabel("Электронная почта", { exact: true }),
  ).toHaveValue(email);
  await expect(
    page.getByRole("heading", { name: "Создать аккаунт" }),
  ).toBeVisible();
  await expect(page.getByLabel("Название магазина")).toHaveCount(0);
  await page.getByRole("button", { name: "Создать аккаунт" }).click();
  await expect(page.getByText(/Если адрес доступен/)).toBeVisible();
  let token = "";
  await expect
    .poll(async () => {
      const response = await page.request.get(
        `${origin}/__test/orders/mail?email=${email}`,
        {
          headers: { "X-Test-Secret": secret },
        },
      );
      expect(response.status()).toBe(200);
      token = (await response.json()).token;
      return token.length;
    })
    .toBeGreaterThan(0);
  await page.goto(`${origin}/#verify=${token}`);
  await page.getByLabel("Новый пароль").fill(password);
  expect(await page.evaluate(() => location.hash)).toBe("");
  await page.getByRole("button", { name: "Подтвердить адрес" }).click();
  await expect(page.getByText(/Почта подтверждена/)).toBeVisible();
  await page.getByLabel("Электронная почта", { exact: true }).fill(email);
  await page.getByLabel("Пароль", { exact: true }).fill(password);
  await page.getByRole("button", { name: "Войти", exact: true }).click();
  await expect(
    page.getByRole("link", { name: "Интеграции", exact: true }),
  ).toBeVisible();
  expect(await page.evaluate(() => document.cookie)).not.toContain(
    "styk_session",
  );
  await page.reload();
  await expect(
    page.getByRole("link", { name: "Заказы", exact: true }),
  ).toBeVisible();
  await expect(page.getByText(/Рабочее пространство/)).toHaveCount(0);
  await page.screenshot({
    path: "test-results/merchant-smoke-desktop.png",
    fullPage: true,
  });
  await page.setViewportSize({ width: 390, height: 844 });
  expect(
    await page.evaluate(() => document.documentElement.scrollWidth),
  ).toBeLessThanOrEqual(391);
  await page.screenshot({
    path: "test-results/merchant-smoke-mobile.png",
    fullPage: true,
  });
  const selector = page.locator(".workspace-selector");
  if ((await selector.getAttribute("open")) === null)
    await selector.locator("summary").click();
  await selector.getByRole("button", { name: "Выйти" }).click();
  await expect(page.getByLabel("Пароль", { exact: true })).toBeVisible();
  expect((await page.request.get(`${origin}/api/session`)).status()).toBe(401);
});
