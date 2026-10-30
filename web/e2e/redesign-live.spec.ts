import { test, expect } from "@playwright/test";

// Opt-in, read-only browser regression against an isolated merchant API.
test("merchant can sign in and open every workspace page against the real API", async ({
  page,
}) => {
  test.skip(
    !process.env.STYK_UI_MERCHANT_EMAIL ||
      !process.env.STYK_UI_MERCHANT_PASSWORD,
    "Set local preview merchant credentials explicitly",
  );
  const origin = process.env.STYK_UI_MERCHANT_ORIGIN || "http://127.0.0.1:5173";
  await page.goto(origin);
  await page
    .getByLabel("Электронная почта")
    .fill(process.env.STYK_UI_MERCHANT_EMAIL!);
  await page
    .getByLabel("Пароль", { exact: true })
    .fill(process.env.STYK_UI_MERCHANT_PASSWORD!);
  await page.getByRole("button", { name: "Войти", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Обзор", exact: true }),
  ).toBeVisible();
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  for (const [name, heading] of [
    ["Товары", "Сопоставление товаров"],
    ["Каталог", "Синхронизация каталога"],
    ["Склады", "Сопоставление складов"],
    ["Интеграции", "Интеграции"],
    ["Диагностика", "Диагностика"],
    ["Обзор", "Обзор"],
  ]) {
    await page
      .locator(".app-tabs")
      .getByRole("link", { name, exact: true })
      .click();
    await expect(
      page.getByRole("heading", { name: heading, exact: true }),
    ).toBeVisible();
    await page.waitForLoadState("networkidle");
    await page.screenshot({
      path: `test-results/redesign/live-${name}.png`,
      fullPage: true,
    });
  }
  await expect(
    page.getByText(/Склады уже сопоставлены для выбранных аккаунтов/),
  ).toBeVisible();
  await expect(
    page.getByRole("link", { name: "Сопоставить склады", exact: true }),
  ).toHaveCount(0);
  await page
    .getByRole("link", { name: "Посмотреть соответствие складов", exact: true })
    .click();
  for (const provider of ["МойСклад", "Ozon"]) {
    await page
      .locator(".read-warehouse-list")
      .filter({
        has: page.getByRole("heading", { name: provider, exact: true }),
      })
      .getByRole("button", { name: "Выбрать для сопоставления", exact: true })
      .first()
      .click();
  }
  const warehouseDialog = page.getByRole("dialog", {
    name: "Сопоставить склады",
    exact: true,
  });
  await expect(
    warehouseDialog.getByText(/Повторное сопоставление не требуется/),
  ).toBeVisible();
  await expect(
    warehouseDialog.getByRole("button", {
      name: "Проверить пару складов",
      exact: true,
    }),
  ).toHaveCount(0);
  await page.screenshot({
    path: "test-results/redesign/live-warehouse-already-mapped.png",
  });
  await warehouseDialog
    .getByRole("button", { name: "Готово", exact: true })
    .click();
  await expect(warehouseDialog).not.toBeVisible();
  expect(errors).toEqual([]);
});
