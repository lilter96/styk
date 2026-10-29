import { expect, test } from "@playwright/test";
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { navigate, selectWorkspace, signIn } from "./workspace";

test("operator accepts a test-only unknown outcome and reviews a separate publication", async ({
  page,
}) => {
  test.skip(
    !process.env.STYK_CONTROLLED_E2E_TEST_API,
    "requires the local recovery harness",
  );
  test.setTimeout(100_000);
  page.setDefaultTimeout(12_000);
  const origin = process.env.STYK_CONTROLLED_E2E_ORIGIN!,
    tenant = "cross-e2e",
    moy = "2928cf35-d30a-11f1-0a80-1b9000000355",
    ozon = "1549477";
  const shots =
    process.env.STYK_CONTROLLED_SCREENSHOTS_DIR || "/tmp/styk-controlled-stock";
  mkdirSync(shots, { recursive: true });
  await page.route("**/*", (route) =>
    ["127.0.0.1", "localhost"].includes(new URL(route.request().url()).hostname)
      ? route.continue()
      : route.abort(),
  );
  await page.setViewportSize({ width: 1440, height: 900 });
  await signIn(page, process.env.STYK_CONTROLLED_E2E_SECRET!, `${origin}/`);
  await selectWorkspace(page, tenant, moy, ozon);
  await navigate(page, "Интеграции");
  await expect(
    page.getByText("Результат уточняется", { exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Разрешить новую проверку" }).click();
  const recovery = page.getByRole("dialog", {
    name: "Принять неопределённость тестовой отправки?",
  });
  await expect(
    recovery.getByText(/Старый запрос может изменить остаток позже/),
  ).toBeVisible();
  await page.screenshot({
    path: join(shots, "recovery-desktop.png"),
    fullPage: true,
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({
    path: join(shots, "recovery-mobile.png"),
    fullPage: true,
  });
  await recovery
    .getByLabel("Причина решения")
    .fill("Оператор принимает неопределённость тестовой отправки");
  await recovery.getByLabel(/прежний worker остановлен/).check();
  await recovery.getByLabel(/Принимаю неопределённость/).check();
  const response = page.waitForResponse(
    (r) => r.url().endsWith("/supersede") && r.request().method() === "POST",
  );
  await recovery
    .getByRole("button", { name: "Записать решение оператора" })
    .click();
  expect((await response).status()).toBe(200);
  await expect(
    page.getByText("Нужна новая проверка", { exact: true }),
  ).toBeVisible();
  expect(
    (
      await page.request.post(
        process.env.STYK_CONTROLLED_E2E_TEST_API! + "/__test/start-worker",
      )
    ).status(),
  ).toBe(204);
  await page
    .getByRole("button", { name: "Включить синхронизацию", exact: true })
    .click();
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
  for (const [name, field, account] of [
    ["МойСклад", "moysklad_account", moy],
    ["Ozon", "ozon_account", ozon],
  ]) {
    const panel = page.getByRole("region", { name: `${name}: импорт данных` });
    await panel.getByRole("button", { name: "Обновить данные" }).click();
    const confirm = page.getByRole("dialog", {
      name: `Загрузить данные ${name}?`,
    });
    await confirm.getByRole("checkbox").check();
    const request = page.waitForResponse(
      (r) =>
        r.url().endsWith("/api/imports") && r.request().method() === "POST",
    );
    await confirm.getByRole("button", { name: "Подтвердить загрузку" }).click();
    const op = (await (await request).json()).operation;
    const query = new URLSearchParams({ tenant, [field]: account });
    await expect
      .poll(
        async () =>
          (
            await (
              await page.request.get(`${origin}/api/imports/${op.id}?${query}`)
            ).json()
          ).state,
        { timeout: 15000 },
      )
      .toBe("SUCCEEDED");
    await expect(
      panel.getByRole("button", { name: "Обновить данные" }),
    ).toBeEnabled({ timeout: 10000 });
  }
  await page
    .getByRole("button", { name: "Включить синхронизацию", exact: true })
    .click();
  dialog = page.getByRole("dialog", {
    name: "Включить синхронизацию остатков?",
  });
  await expect(dialog.getByText(/Условия отправки подтверждены/)).toBeVisible();
  await dialog.getByText("Проверенные товары и склады").click();
  await expect(dialog.getByText(/к отправке 7 шт/)).toBeVisible();
  await dialog
    .getByLabel("Подтверждаю изменение остатков в Ozon для выбранных аккаунтов")
    .check();
  await dialog.getByRole("button", { name: "Отправить остаток" }).click();
  await expect(
    page.getByText("Последняя отправка сверена", { exact: true }),
  ).toBeVisible({ timeout: 50_000 });
  await expect(page.getByText(/Проверено в Ozon: 7 шт/)).toBeVisible();
});
