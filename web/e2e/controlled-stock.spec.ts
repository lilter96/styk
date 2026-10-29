import { expect, test } from "@playwright/test";
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { navigate, selectWorkspace, signIn } from "./workspace";

test("operator reviews and publishes a single controlled quantity through the worker", async ({
  page,
}) => {
  test.skip(
    !process.env.STYK_CONTROLLED_E2E_ORIGIN,
    "requires local synthetic provider and worker",
  );
  test.setTimeout(90_000);
  page.setDefaultTimeout(10_000);
  const origin = process.env.STYK_CONTROLLED_E2E_ORIGIN!;
  const tenant = "cross-e2e",
    moy = "2928cf35-d30a-11f1-0a80-1b9000000355",
    ozon = "1549477";
  const shots =
    process.env.STYK_CONTROLLED_SCREENSHOTS_DIR || "/tmp/styk-controlled-stock";
  mkdirSync(shots, { recursive: true });
  await page.route("**/*", (route) => {
    const host = new URL(route.request().url()).hostname;
    return ["127.0.0.1", "localhost"].includes(host)
      ? route.continue()
      : route.abort();
  });
  await page.setViewportSize({ width: 1440, height: 900 });
  await signIn(page, process.env.STYK_CONTROLLED_E2E_SECRET!, `${origin}/`);
  await selectWorkspace(page, tenant, moy, ozon);
  await navigate(page, "Интеграции");
  const importBoth = async () => {
    for (const [name, field, account] of [
      ["МойСклад", "moysklad_account", moy],
      ["Ozon", "ozon_account", ozon],
    ]) {
      const panel = page.getByRole("region", {
        name: `${name}: импорт данных`,
      });
      await panel.getByRole("button", { name: "Обновить данные" }).click();
      const confirmation = page.getByRole("dialog", {
        name: `Загрузить данные ${name}?`,
      });
      await confirmation.getByRole("checkbox").check();
      const submitted = page.waitForResponse(
        (r) =>
          r.url().endsWith("/api/imports") && r.request().method() === "POST",
      );
      await confirmation
        .getByRole("button", { name: "Подтвердить загрузку" })
        .click();
      const result = await (await submitted).json();
      const query = new URLSearchParams({ tenant, [field]: account });
      await expect
        .poll(
          async () =>
            (
              await (
                await page.request.get(
                  `${origin}/api/imports/${result.operation.id}?${query}`,
                )
              ).json()
            ).state,
          { timeout: 15000 },
        )
        .toBe("SUCCEEDED");
      await expect(
        panel.getByRole("button", { name: "Обновить данные" }),
      ).toBeEnabled({ timeout: 10000 });
    }
  };
  await importBoth();
  await navigate(page, "Товары");
  await page.getByRole("button", { name: /^Сопоставить / }).click();
  await page.getByLabel("Поиск товара МойСклад").fill("Тестовый товар");
  await page.getByRole("button", { name: "Тестовый товар, товар" }).click();
  await page.getByRole("button", { name: "Проверить соответствие" }).click();
  await expect(page.getByText("Проверка готова к подтверждению")).toBeVisible();
  await page.getByLabel("Подтверждаю, что это один и тот же товар").check();
  await page.getByRole("button", { name: "Подтвердить соответствие" }).click();
  await expect(page.getByText(/Соответствие.*сохранено/)).toBeVisible();
  await page.getByRole("button", { name: "Закрыть сопоставление" }).click();
  await navigate(page, "Склады");
  await page
    .locator(".warehouse-row")
    .filter({ hasText: "Тестовый склад" })
    .getByRole("button", { name: "Выбрать для сопоставления" })
    .click();
  await page
    .locator(".warehouse-row")
    .filter({ hasText: "Тестовый FBS" })
    .getByRole("button", { name: "Выбрать для сопоставления" })
    .click();
  const warehouses = page.getByRole("region", {
    name: "Проверка соответствия складов",
  });
  await warehouses
    .getByRole("button", { name: "Проверить пару складов" })
    .click();
  await expect(
    warehouses.getByText("Проверка складов готова к подтверждению"),
  ).toBeVisible();
  await warehouses
    .getByLabel("Подтверждаю соответствие выбранных складов")
    .check();
  await warehouses
    .getByRole("button", { name: "Подтвердить соответствие складов" })
    .click();
  await expect(page.getByText(/Соответствие.*сохранено/)).toBeVisible();
  await page
    .getByRole("button", { name: "Закрыть сопоставление складов" })
    .click();
  await navigate(page, "Интеграции");
  await page.getByRole("button", { name: "Включить синхронизацию" }).click();
  let dialog = page.getByRole("dialog", {
    name: "Включить синхронизацию остатков?",
  });
  await dialog.getByLabel(/Подтверждаю эти тестовые допущения/).check();
  await dialog
    .getByRole("button", { name: "Записать тестовые допущения" })
    .click();
  await expect(
    dialog.getByText(/Допущения записаны как утверждение оператора/),
  ).toBeVisible();
  await dialog.getByRole("button", { name: "Отмена" }).click();
  await importBoth();
  await page.getByRole("button", { name: "Включить синхронизацию" }).click();
  dialog = page.getByRole("dialog", {
    name: "Включить синхронизацию остатков?",
  });
  await expect(dialog.getByText(/Условия отправки подтверждены/)).toBeVisible();
  await dialog.getByText("Проверенные товары и склады").click();
  await expect(dialog.getByText(/к отправке 7 шт/)).toBeVisible();
  await expect(dialog.getByText(/МойСклад: 7 шт/)).toBeVisible();
  await page.screenshot({
    path: join(shots, "review-desktop.png"),
    fullPage: true,
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({
    path: join(shots, "review-mobile.png"),
    fullPage: true,
  });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  await dialog
    .getByLabel("Подтверждаю изменение остатков в Ozon для выбранных аккаунтов")
    .check();
  await dialog.getByRole("button", { name: "Отправить остаток" }).click();
  await expect(
    page.getByText("Последняя отправка сверена", { exact: true }),
  ).toBeVisible({ timeout: 15000 });
  await expect(page.getByText(/Проверено в Ozon: 7 шт/)).toBeVisible();
  await page.screenshot({
    path: join(shots, "result-mobile.png"),
    fullPage: true,
  });
});
