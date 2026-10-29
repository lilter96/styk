import { expect, test } from "@playwright/test";
import { signIn, navigate } from "./workspace";

test("explicit local registration uses bounded synthetic providers", async ({
  page,
  request,
}) => {
  const api = process.env.STYK_E2E_API_URL!;
  const origin = "http://127.0.0.1:15174";
  const jsonResponses: Promise<string>[] = [];
  page.on("response", (response) => {
    if (
      response.url().includes("/api/") &&
      (response.headers()["content-type"] || "").includes("application/json")
    ) {
      jsonResponses.push(response.text().catch(() => ""));
    }
  });
  const body = { tenant: "browser-tenant", acknowledge: true };
  expect(
    (
      await request.post(`${api}/api/integrations/moysklad/register`, {
        headers: { Origin: origin },
        data: body,
      })
    ).status(),
  ).toBe(401);
  await signIn(page, "ssssssssssssssssssssssssssssssss", origin);
  await page.evaluate(() => sessionStorage.setItem("styk-selected-accounts", JSON.stringify({ tenant: "browser-tenant", moy: "", ozon: "" })));
  await page.reload();
  await navigate(page, "Интеграции");
  const moy = page
    .locator("article")
    .filter({ has: page.getByRole("heading", { name: "МойСклад" }) });
  await expect(moy.getByText("Не подключено", { exact: true })).toBeVisible();
  await moy
    .getByRole("button", { name: /^(Подключить|Настройки|Выбрать аккаунт)$/ })
    .click();
  const moyDialog = page.getByRole("dialog", { name: "Подключение МойСклад" });
  await moyDialog
    .getByText("Проверка доступа и настройки сервера", { exact: true })
    .click();
  await expect(
    moyDialog.getByRole("button", { name: "Добавить аккаунт" }),
  ).toBeDisabled();
  const response = await page.request.get(
    `${api}/api/integrations?tenant=browser-tenant`,
  );
  const status = await response.text();
  expect(status).not.toContain("synthetic-only-token");
  expect(status).not.toContain("synthetic-only-key");
  const providers = JSON.parse(status).providers as {
    provider: string;
    account: string;
  }[];
  const configuredMoy = providers.find(
    (p) => p.provider === "moysklad",
  )!.account;
  const configuredOzon = providers.find((p) => p.provider === "ozon")!.account;
  await moyDialog
    .getByRole("button", { name: "Проверить подключение" })
    .click();
  await expect(moyDialog.getByText(/Доступ проверен/)).toBeVisible();
  await moyDialog
    .locator(".legacy-connection-settings")
    .getByText("Технические детали", { exact: true })
    .click();
  await expect(
    moyDialog.getByText("MATCHED_RETURNED_PRODUCT", { exact: false }),
  ).toBeVisible();
  let attempts = 0;
  await page.route("**/api/integrations/moysklad/register", async (route) => {
    attempts++;
    await route.fetch();
    await route.abort("failed");
  });
  await moyDialog
    .locator(".legacy-connection-settings")
    .getByLabel(/Подтверждаю, что этот аккаунт/)
    .check();
  await moyDialog.getByRole("button", { name: "Добавить аккаунт" }).click();
  await expect(page.getByText(/Результат добавления неизвестен/)).toBeVisible();
  await expect(
    moyDialog.getByRole("button", { name: "Добавить аккаунт" }),
  ).toBeDisabled();
  await moyDialog.getByRole("button", { name: "Закрыть настройки" }).click();
  await page
    .getByRole("button", { name: "Проверить результат добавления" })
    .click();
  await moy
    .getByRole("button", { name: "Выбрать аккаунт", exact: true })
    .click();
  await moyDialog
    .getByRole("combobox", { name: "МойСклад: аккаунт" })
    .selectOption(configuredMoy);
  await moyDialog.getByRole("button", { name: "Закрыть настройки" }).click();
  await expect(moy.locator(".connection-state")).toContainText("Работает");
  expect(attempts).toBe(1);
  const ozon = page
    .locator("article")
    .filter({ has: page.getByRole("heading", { name: "Ozon" }) });
  await ozon
    .getByRole("button", { name: /^(Подключить|Настройки|Выбрать аккаунт)$/ })
    .click();
  const ozonDialog = page.getByRole("dialog", { name: "Подключение Ozon" });
  await ozonDialog
    .getByText("Проверка доступа и настройки сервера", { exact: true })
    .click();
  await ozonDialog
    .getByRole("button", { name: "Проверить подключение" })
    .click();
  await expect(ozonDialog.getByText(/Доступ проверен/)).toBeVisible();
  await ozonDialog
    .locator(".legacy-connection-settings")
    .getByText("Технические детали", { exact: true })
    .click();
  await expect(
    ozonDialog.getByText("NOT_ESTABLISHED", { exact: false }),
  ).toBeVisible();
  await ozonDialog
    .locator(".legacy-connection-settings")
    .getByLabel(/Подтверждаю, что этот аккаунт/)
    .check();
  await ozonDialog.getByRole("button", { name: "Добавить аккаунт" }).click();
  await ozonDialog
    .getByRole("combobox", { name: "Ozon: аккаунт" })
    .selectOption(configuredOzon);
  await ozonDialog.getByRole("button", { name: "Закрыть настройки" }).click();
  await expect(ozon.locator(".connection-state")).toContainText("Работает");
  await page.reload();
  await expect(moy.locator(".connection-state")).toContainText("Работает");
  await expect(ozon.locator(".connection-state")).toContainText("Работает");
  await moy.getByText("Настройки", { exact: true }).click();
  const moySelector = moyDialog.getByRole("combobox", {
    name: "МойСклад: аккаунт",
  });
  const ozonSelector = ozonDialog.getByRole("combobox", {
    name: "Ozon: аккаунт",
  });
  expect(configuredMoy).toMatch(/^synthetic-moy-browser-/);
  expect(configuredOzon).toMatch(/^synthetic-ozon-browser-/);
  await moySelector.selectOption(configuredMoy);
  await expect(moySelector).toHaveValue(configuredMoy);
  await moyDialog.getByRole("button", { name: "Закрыть настройки" }).click();
  await ozon.getByText("Настройки", { exact: true }).click();
  await ozonSelector.selectOption(configuredOzon);
  await expect(ozonSelector).toHaveValue(configuredOzon);
  await ozonDialog.getByRole("button", { name: "Закрыть настройки" }).click();
  await page.evaluate(() => sessionStorage.setItem("styk-selected-accounts", JSON.stringify({ tenant: "other-tenant", moy: "", ozon: "" })));
  await page.reload();
  await expect(moy.locator(".connection-state")).toContainText(
    "Нужно действие",
  );
  await moy.getByRole("button", { name: "Подключить", exact: true }).click();
  await expect(moy.getByText(/другой учётной записи STYK/)).toBeVisible();
  await expect(
    moyDialog.getByRole("button", { name: "Добавить аккаунт", exact: true }),
  ).toHaveCount(0);
  await moyDialog.getByRole("button", { name: "Закрыть настройки" }).click();
  await navigate(page, "Товары");
  await expect(
    page.getByRole("heading", { name: "Сопоставление товаров" }),
  ).toBeVisible();
  await navigate(page, "Склады");
  await expect(
    page.getByRole("heading", { name: "Сопоставление складов" }),
  ).toBeVisible();
  const observedResponses = (await Promise.all(jsonResponses)).join("\n");
  expect(observedResponses).not.toContain("synthetic-only-key");
  expect(observedResponses).not.toContain("synthetic-only-token");
});
