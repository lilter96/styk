import { test, expect } from "@playwright/test";
import { signIn, selectWorkspace } from "./workspace";
const origin = process.env.STYK_SETTINGS_ORIGIN || "http://127.0.0.1:15179";
const scope = JSON.parse(process.env.STYK_WAREHOUSE_SCOPE || "{}");
test("warehouse read, search, pages, blocked capability and accessible drawer", async ({
  page,
}) => {
  await signIn(page, origin);
  await selectWorkspace(page, scope.tenant, scope.moy, scope.ozon);
  await page.goto(origin + "/warehouses");
  await expect(page.getByRole("button", { name: "Все (57)" })).toBeVisible();
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.screenshot({
    path: "test-results/warehouses/overview-1440.png",
    fullPage: true,
  });
  await page.getByRole("button", { name: "Следующая страница" }).click();
  await expect(page.getByLabel("Текущая страница")).toHaveText("2");
  await page.getByLabel("Поиск склада").fill("Регион 053");
  await expect(page.locator(".wh-table tbody tr")).toHaveCount(1);
  await expect(page.getByRole("link", { name: "Регион 053" })).toBeVisible();
  await expect(page.getByLabel("Текущая страница")).toHaveText("1");
  await page.getByRole("button", { name: "Сопоставить", exact: true }).click();
  const dialog = page.getByRole("dialog", {
    name: "Сопоставить склады",
    exact: true,
  });
  await expect(
    dialog.getByRole("radio", { name: /Основной склад/ }),
  ).toBeDisabled();
  await dialog.getByRole("radio", { name: /Резервный склад/ }).check();
  await expect(
    dialog.getByRole("button", { name: "Проверить соответствие" }),
  ).toBeEnabled();
  await page.screenshot({ path: "test-results/warehouses/select-1440.png" });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({ path: "test-results/warehouses/select-390.png" });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);

  const conflict = await page.request.post(
    process.env.STYK_SETTINGS_TEST_API + "/__test/warehouse/conflict",
  );
  expect(conflict.status()).toBe(204);
  await dialog.getByRole("button", { name: "Проверить соответствие" }).click();
  await expect(
    dialog.getByText("Обнаружен конфликт сопоставлений"),
  ).toBeVisible();
  await expect(
    dialog.getByRole("button", { name: "Подтвердить сопоставление" }),
  ).toHaveCount(0);
  await page.screenshot({ path: "test-results/warehouses/conflict-390.png" });
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.screenshot({ path: "test-results/warehouses/conflict-1440.png" });
  await page.keyboard.press("Escape");
  await expect(dialog).toHaveCount(0);
  await expect(
    page.getByRole("button", { name: "Сопоставить", exact: true }),
  ).toBeFocused();
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.getByLabel("Поиск склада").fill("Недоступный rFBS");
  await page.getByRole("button", { name: "Проверить", exact: true }).click();
  await dialog.getByRole("radio", { name: /Свободный склад/ }).check();
  await dialog.getByRole("button", { name: "Проверить соответствие" }).click();
  await expect(
    dialog.getByText("Возможность сопоставления не подтверждена"),
  ).toBeVisible();
  await expect(
    dialog.getByRole("button", { name: "Подтвердить сопоставление" }),
  ).toHaveCount(0);
  await page.screenshot({ path: "test-results/warehouses/blocked-1440.png" });
  await page.keyboard.press("Escape");
  await page.getByLabel("Поиск склада").fill("Склад без типа");
  await page.getByRole("button", { name: "Проверить", exact: true }).click();
  await dialog.getByRole("radio", { name: /Свободный склад/ }).check();
  await dialog.getByRole("button", { name: "Проверить соответствие" }).click();
  await expect(dialog.getByText("Не хватает данных о складах")).toBeVisible();
  await page.keyboard.press("Escape");
  await page.getByLabel("Поиск склада").fill("");
  await page.getByRole("button", { name: /Сопоставлены \([123]\)/ }).click();
  const mapped = await page.request.get(
    origin +
      "/api/warehouses?" +
      new URLSearchParams({
        view: "overview",
        tenant: scope.tenant,
        moysklad_account: scope.moy,
        ozon_account: scope.ozon,
      }),
  );
  await expect(page.locator(".wh-table tbody tr")).toHaveCount(
    (await mapped.json()).counts.mapped,
  );
});
