import { test, expect } from "@playwright/test";
import { signIn, selectWorkspace } from "./workspace";
const origin = process.env.STYK_SETTINGS_ORIGIN || "http://127.0.0.1:15179";
const scope = JSON.parse(process.env.STYK_WAREHOUSE_SCOPE || "{}");
test("warehouse confirmation preserves stale guards, exact retry, order visibility and source stocks", async ({
  page,
}) => {
  let requests = 0;
  const bodies: string[] = [];
  await page.route(
    "**/api/warehouse-mapping-reviews/*/confirm",
    async (route) => {
      requests++;
      bodies.push(route.request().postData() || "");
      if (requests === 1) {
        await route.fetch();
        await route.abort("failed");
      } else await route.continue();
    },
  );
  await signIn(page, origin);
  await selectWorkspace(page, scope.tenant, scope.moy, scope.ozon);
  await page.goto(
    origin + "/warehouses?search=" + encodeURIComponent("Регион 000"),
  );
  await page.getByRole("button", { name: "Сопоставить", exact: true }).click();
  const dialog = page.getByRole("dialog", {
    name: "Сопоставить склады",
    exact: true,
  });
  await dialog.getByRole("radio", { name: /Склад Екатеринбург/ }).check();
  await dialog.getByRole("button", { name: "Проверить соответствие" }).click();
  await expect(
    dialog.getByText("Можно сопоставить", { exact: true }),
  ).toBeVisible();
  const confirm = dialog.getByRole("button", {
    name: "Подтвердить сопоставление",
    exact: true,
  });
  await expect(confirm).toBeDisabled();
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.screenshot({ path: "test-results/warehouses/review-1440.png" });
  expect(requests).toBe(0);
  const stale = await page.request.post(
    process.env.STYK_SETTINGS_TEST_API + "/__test/warehouse/stale",
  );
  expect(stale.status()).toBe(204);
  await expect(
    dialog.getByText("Проверка устарела", { exact: true }),
  ).toBeVisible({ timeout: 12000 });
  await expect(confirm).toBeDisabled();
  await dialog.getByRole("button", { name: "Повторить проверку" }).click();
  await expect(
    dialog.getByText("Можно сопоставить", { exact: true }),
  ).toBeVisible();
  await expect(confirm).toBeDisabled();
  await dialog
    .getByRole("checkbox", {
      name: "Подтверждаю, что эти склады соответствуют друг другу",
    })
    .check();
  await expect(confirm).toBeEnabled();
  await confirm.click();
  await expect(
    dialog.getByText("Результат сохранения уточняется", { exact: true }),
  ).toBeVisible();
  await page.keyboard.press("Escape");
  await page
    .getByRole("button", { name: "Повторить то же подтверждение", exact: true })
    .click();
  await expect(
    page.getByText(/Соответствие подтверждено/).first(),
  ).toBeVisible();
  expect(bodies).toHaveLength(2);
  expect(bodies[0]).toBe(bodies[1]);
  await expect(
    page.locator(".wh-table").getByText("Склад Екатеринбург", { exact: true }),
  ).toBeVisible();
  const query = new URLSearchParams({
    tenant: scope.tenant,
    moysklad_account: scope.moy,
    ozon_account: scope.ozon,
  });
  const orders = await page.request.get(
    origin + "/api/orders/warehouses?" + query,
  );
  expect(orders.status()).toBe(200);
  const orderData = await orders.json();
  expect(orderData.confirmed).toBe(2);
  await page.getByRole("link", { name: "Открыть", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Остатки на складе", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("columnheader", { name: "Свободно", exact: true }),
  ).toBeVisible();
  await expect(page.locator("td.wh-number").first()).toHaveText("0");
  await page.getByRole("button", { name: "МойСклад", exact: true }).click();
  await expect(
    page.getByRole("columnheader", { name: "В пути", exact: true }),
  ).toBeVisible();
  await expect(page.locator("td.wh-number").last()).toHaveText("—");
  await page.screenshot({
    path: "test-results/warehouses/stock-1440.png",
    fullPage: true,
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({
    path: "test-results/warehouses/stock-390.png",
    fullPage: true,
  });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  await page.reload();
  await expect(
    page.getByRole("heading", { name: "Регион 000", exact: true }),
  ).toBeVisible();
  await page.getByRole("link", { name: "Ко всем складам" }).click();
  await expect(page.getByLabel("Поиск склада")).toHaveValue("Регион 000");
  await page.goBack();
  await expect(
    page.getByRole("heading", { name: "Регион 000", exact: true }),
  ).toBeVisible();
  await page.goForward();
  await expect(
    page.getByRole("heading", { name: "Склады", exact: true }),
  ).toBeVisible();
});
