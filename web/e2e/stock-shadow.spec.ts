import { test, expect } from "@playwright/test";
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { signIn, selectWorkspace, navigate } from "./workspace";

test("read-only stock inspection uses the real API and keeps marketplace writes disabled", async ({
  page,
}) => {
  test.skip(
    !process.env.STYK_SHADOW_E2E_ORIGIN,
    "requires the synthetic Go API and database",
  );
  const origin = process.env.STYK_SHADOW_E2E_ORIGIN!;
  const tenant = process.env.STYK_SHADOW_E2E_TENANT!;
  const moy = process.env.STYK_SHADOW_E2E_MOY!;
  const ozon = process.env.STYK_SHADOW_E2E_OZON!;
  const shots =
    process.env.STYK_SHADOW_SCREENSHOTS_DIR || "/tmp/styk-stock-shadow";
  mkdirSync(shots, { recursive: true });
  const forbidden: string[] = [];
  const shadowScopes: string[] = [];
  page.on("request", (request) => {
    const url = new URL(request.url());
    if (url.pathname === "/api/stock-sync/shadow")
      shadowScopes.push(url.search);
    if (
      url.pathname === "/v2/products/stocks" ||
      (url.pathname.startsWith("/api/stock-sync/") &&
        request.method() !== "GET")
    )
      forbidden.push(`${request.method()} ${url.pathname}`);
  });
  await page.setViewportSize({ width: 1440, height: 900 });
  await signIn(page, process.env.STYK_SHADOW_E2E_SECRET!, `${origin}/`);
  await selectWorkspace(page, tenant, moy, ozon);
  await navigate(page, "Интеграции");
  await page.getByRole("button", { name: "Проверить остатки" }).click();
  let dialog = page.getByRole("dialog", { name: "Проверка остатков" });
  await expect(
    dialog.getByText("Синтетическая футболка", { exact: true }),
  ).toBeVisible();
  await expect(
    dialog.getByText("Синтетическая модель", { exact: false }),
  ).toBeVisible();
  const shirt = dialog
    .locator(".shadow-target")
    .filter({ hasText: "Синтетическая футболка" });
  await expect(shirt.getByText("10", { exact: true })).toBeVisible();
  await expect(shirt.getByText("1", { exact: true })).toBeVisible();
  await expect(shirt.getByText("4", { exact: true })).toBeVisible();
  await expect(
    shirt.getByText("Соответствия товара и склада подтверждены"),
  ).toBeVisible();
  await expect(shirt.getByText("Расчёт выполнен")).toBeVisible();
  await expect(shirt.getByText("8", { exact: true })).toBeVisible();
  await expect(
    shirt.getByText("Разница с Ozon: 4.", { exact: false }),
  ).toBeVisible();
  const jacket = dialog
    .locator(".shadow-target")
    .filter({ hasText: "Синтетическая куртка" });
  await expect(
    jacket.getByText("Нет данных об остатке на складе МойСклад"),
  ).toBeVisible();
  await expect(
    jacket.getByText("Нет данных", { exact: true }).first(),
  ).toBeVisible();
  await page.screenshot({
    path: join(shots, "overview-desktop.png"),
    fullPage: true,
  });
  await jacket
    .getByText("Нет данных об остатке на складе МойСклад")
    .scrollIntoViewIfNeeded();
  await page.screenshot({
    path: join(shots, "missing-desktop.png"),
    fullPage: true,
  });
  await jacket.getByText("Технические детали").click();
  await expect(jacket.getByText(/SOURCE_OBSERVATION_MISSING/)).toBeVisible();
  await jacket.getByText(/SOURCE_OBSERVATION_MISSING/).scrollIntoViewIfNeeded();
  await page.screenshot({
    path: join(shots, "blocked-details-desktop.png"),
    fullPage: true,
  });
  await jacket.getByText("Технические детали").click();
  await shirt.scrollIntoViewIfNeeded();
  await shirt.getByText("Технические детали").click();
  await expect(shirt.getByText(/Проверки:/)).toBeVisible();
  await shirt.getByText(/Проверки:/).scrollIntoViewIfNeeded();
  await page.screenshot({
    path: join(shots, "details-desktop.png"),
    fullPage: true,
  });
  await shirt.getByText("Технические детали").click();
  await page.setViewportSize({ width: 390, height: 844 });
  await dialog
    .locator(".provider-settings-dialog-content")
    .evaluate((element) => {
      element.scrollTop = 0;
    });
  await page.screenshot({
    path: join(shots, "overview-mobile.png"),
    fullPage: true,
  });
  await jacket
    .getByText("Нет данных об остатке на складе МойСклад")
    .scrollIntoViewIfNeeded();
  await page.screenshot({
    path: join(shots, "missing-mobile.png"),
    fullPage: true,
  });
  await jacket.getByText("Технические детали").click();
  await jacket.getByText(/SOURCE_OBSERVATION_MISSING/).scrollIntoViewIfNeeded();
  await page.screenshot({
    path: join(shots, "blocked-details-mobile.png"),
    fullPage: true,
  });
  await jacket.getByText("Технические детали").click();
  await shirt.scrollIntoViewIfNeeded();
  await shirt.getByText("Технические детали").click();
  await shirt.getByText(/Проверки:/).scrollIntoViewIfNeeded();
  await page.screenshot({
    path: join(shots, "details-mobile.png"),
    fullPage: true,
  });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
  await dialog.getByRole("link", { name: "Сопоставить товары" }).click();
  await expect(page).toHaveURL(/\/products/);
  await navigate(page, "Интеграции");
  await page.getByRole("button", { name: "Проверить остатки" }).click();
  dialog = page.getByRole("dialog", { name: "Проверка остатков" });
  await expect(
    dialog.getByText("Синтетическая футболка", { exact: true }),
  ).toBeVisible();
  await dialog.getByRole("button", { name: "Обновить проверку" }).click();
  await page.reload();
  await selectWorkspace(page, tenant, moy, ozon);
  await navigate(page, "Интеграции");
  await page.getByRole("button", { name: "Проверить остатки" }).click();
  await expect(
    page
      .getByRole("dialog", { name: "Проверка остатков" })
      .getByText("Синтетическая футболка", { exact: true }),
  ).toBeVisible();
  expect(shadowScopes.length).toBeGreaterThanOrEqual(3);
  for (const query of shadowScopes) {
    const scope = new URLSearchParams(query);
    expect(scope.get("tenant")).toBe(tenant);
    expect(scope.get("moysklad_account")).toBe(moy);
    expect(scope.get("ozon_account")).toBe(ozon);
  }
  expect(forbidden).toEqual([]);
});
