import { expect, test, type Page } from "@playwright/test";

const origin = process.env.STYK_ORDERS_STACK_ORIGIN || "";
const serializedScope = process.env.STYK_ORDERS_STACK_SCOPE || "";
const password = "synthetic-orders-process-password";

test("orders review reaches real API replicas and durable worker readback", async ({ browser }) => {
  test.skip(!origin || !serializedScope, "requires the nine-process isolated orders stack");
  test.setTimeout(90_000);
  expect(new URL(origin).hostname).toBe("127.0.0.1");
  const scope = JSON.parse(serializedScope) as {
    tenant: string;
    moysklad_account: string;
    ozon_account: string;
  };
  const contexts = await Promise.all([browser.newContext(), browser.newContext()]);
  try {
    const [a, b] = await Promise.all(contexts.map((context) => context.newPage()));
    for (const page of [a, b]) {
      await page.route("**/*", (route) =>
        new URL(route.request().url()).hostname === "127.0.0.1"
          ? route.continue()
          : route.abort(),
      );
    }
    await login(a, "orders-process-a@example.test");
    await login(b, "orders-process-b@example.test");
    const selector = a.locator(".workspace-selector");
    if ((await selector.getAttribute("open")) === null) await selector.locator("summary").click();
    await selector.getByRole("combobox", { name: "МойСклад", exact: true }).selectOption(scope.moysklad_account);
    await selector.getByRole("combobox", { name: "Ozon", exact: true }).selectOption(scope.ozon_account);
    await a.getByRole("link", { name: "Заказы", exact: true }).click();
    await expect(a.getByRole("heading", { name: "Заказы", exact: true })).toBeVisible();
    await a.getByLabel("Найти отправление или заказ").fill("browser-create");
    await a.getByLabel("Выбрать отправление browser-create", { exact: true }).check();
    await a.getByRole("button", { name: "Проверить выбранные отправления", exact: true }).click();
    await expect(a.getByRole("heading", { name: "Проверка перед передачей" })).toBeVisible();
    await a.getByLabel("Подтвердить отправление browser-create", { exact: true }).check();
    const submit = a.getByRole("button", { name: "Передать в МойСклад (1)", exact: true });
    await expect(submit).toBeDisabled();
    await a.getByLabel(/Я проверил состав и поля выбранных отправлений/).check();
    await expect(submit).toBeEnabled();
    await submit.click();
    await expect(a.getByText("Подтверждение записано.", { exact: false })).toBeVisible();
    await expect.poll(async () => {
      await a.getByRole("button", { name: "Обновить статусы заказов", exact: true }).click();
      return a.locator(".orders-work").filter({ hasText: "browser-create" }).getByText("Подтверждено сверкой", { exact: true }).count();
    }, { timeout: 25_000 }).toBe(1);
    await a.reload();
    await expect(a.locator(".orders-work").filter({ hasText: "browser-create" })).toContainText("Подтверждено сверкой");
    const query = new URLSearchParams(scope).toString();
    for (const resource of ["postings", "operations", "config", "metadata"]) {
      const response = await b.request.get(`${origin}/api/orders/${resource}?${query}`);
      expect(response.status()).toBe(404);
      expect(await response.text()).not.toContain("browser-create");
    }
    await a.setViewportSize({ width: 390, height: 844 });
    expect(await a.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1)).toBe(true);
  } finally {
    await Promise.all(contexts.map((context) => context.close()));
  }
});

async function login(page: Page, email: string) {
  await page.goto(origin);
  await page.getByLabel("Электронная почта", { exact: true }).fill(email);
  await page.getByLabel("Пароль", { exact: true }).fill(password);
  await page.getByRole("button", { name: "Войти", exact: true }).click();
  await expect(page.getByRole("link", { name: "Заказы", exact: true })).toBeVisible();
}
