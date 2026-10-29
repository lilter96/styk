import { expect, test } from "@playwright/test";
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { navigate, selectWorkspace, signIn } from "./workspace";

test("automatic test policy imports, publishes, reconciles, avoids a duplicate and pauses", async ({
  page,
}) => {
  test.skip(
    !process.env.STYK_CONTROLLED_E2E_ORIGIN,
    "requires local synthetic provider and worker",
  );
  test.setTimeout(90_000);
  const origin = process.env.STYK_CONTROLLED_E2E_ORIGIN!;
  const shots =
    process.env.STYK_CONTROLLED_SCREENSHOTS_DIR ||
    "/tmp/styk-controlled-automatic";
  mkdirSync(shots, { recursive: true });
  await page.route("**/*", (route) =>
    ["127.0.0.1", "localhost"].includes(new URL(route.request().url()).hostname)
      ? route.continue()
      : route.abort(),
  );
  await page.setViewportSize({ width: 1440, height: 900 });
  await signIn(page, process.env.STYK_CONTROLLED_E2E_SECRET!, `${origin}/`);
  await selectWorkspace(
    page,
    "cross-e2e",
    "2928cf35-d30a-11f1-0a80-1b9000000355",
    "1549477",
  );
  await navigate(page, "Интеграции");
  const card = page.getByRole("region", {
    name: "Автоматическая синхронизация",
  });
  await card
    .getByRole("button", { name: "Настроить автоматическую синхронизацию" })
    .click();
  const dialog = page.getByRole("dialog", {
    name: "Настроить автоматическую синхронизацию?",
  });
  await dialog.getByLabel(/Подтверждаю выбранные настройки/).check();
  await dialog
    .getByRole("button", { name: "Включить автоматическую проверку" })
    .click();
  await expect(
    card.getByText("Последняя отправка сверена с данными Ozon."),
  ).toBeVisible({ timeout: 30000 });
  await expect(card.getByText(/МойСклад: 7 шт/)).toBeVisible();
  await expect(card.getByText(/Ozon: 7/)).toBeVisible();
  await page.screenshot({ path: join(shots, "desktop.png"), fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({ path: join(shots, "mobile.png"), fullPage: true });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  await card.getByRole("button", { name: "Проверить сейчас" }).click();
  await expect(
    card.getByText("Остатки совпадают — отправка не потребовалась."),
  ).toBeVisible({ timeout: 30000 });
  await card
    .getByRole("button", { name: "Остановить автоматическую проверку" })
    .click();
  await expect(card.getByText("Выключена", { exact: true })).toBeVisible();
});
