import { expect, test } from "@playwright/test";

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
  await page.goto(origin);
  await page
    .getByLabel("Operator secret")
    .fill("ssssssssssssssssssssssssssssssss");
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.getByLabel("Tenant", { exact: true }).fill("browser-tenant");
  await page.getByRole("button", { name: "Open workspace" }).click();
  await page
    .getByRole("navigation")
    .getByRole("link", { name: "Integrations" })
    .click();
  const moy = page
    .locator("article")
    .filter({ has: page.getByRole("heading", { name: "МойСклад" }) });
  await expect(
    moy.getByText("Данные доступа настроены · доступ не проверен"),
  ).toBeVisible();
  await moy.getByText("Настройки", { exact: true }).click();
  await expect(
    moy.getByRole("button", { name: "Добавить аккаунт" }),
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
  await moy.getByRole("button", { name: "Проверить подключение" }).click();
  await expect(moy.getByText(/Доступ проверен/)).toBeVisible();
  await expect(
    moy.getByText(/Полученный товар соответствует настроенному аккаунту/),
  ).toBeVisible();
  let attempts = 0;
  await page.route("**/api/integrations/moysklad/register", async (route) => {
    attempts++;
    await route.fetch();
    await route.abort("failed");
  });
  await moy.getByLabel(/Подтверждаю, что этот аккаунт/).check();
  await moy.getByRole("button", { name: "Добавить аккаунт" }).click();
  await expect(page.getByText(/Результат добавления неизвестен/)).toBeVisible();
  await expect(
    moy.getByRole("button", { name: "Добавить аккаунт" }),
  ).toBeDisabled();
  await page
    .getByRole("button", { name: "Проверить результат добавления" })
    .click();
  await expect(
    moy.getByText("Аккаунт добавлен", { exact: true }),
  ).toBeVisible();
  expect(attempts).toBe(1);
  const ozon = page
    .locator("article")
    .filter({ has: page.getByRole("heading", { name: "Ozon" }) });
  await ozon.getByText("Настройки", { exact: true }).click();
  await ozon.getByRole("button", { name: "Проверить подключение" }).click();
  await expect(ozon.getByText(/Доступ проверен/)).toBeVisible();
  await expect(
    ozon.getByText(/Принадлежность аккаунта не подтверждена независимо/),
  ).toBeVisible();
  await ozon.getByLabel(/Подтверждаю, что этот аккаунт/).check();
  await ozon.getByRole("button", { name: "Добавить аккаунт" }).click();
  await expect(
    ozon.getByText("Аккаунт добавлен", { exact: true }),
  ).toBeVisible();
  await page.reload();
  await expect(
    moy.getByText("Аккаунт добавлен", { exact: true }),
  ).toBeVisible();
  await expect(
    ozon.getByText("Аккаунт добавлен", { exact: true }),
  ).toBeVisible();
  await moy.getByText("Настройки", { exact: true }).click();
  await ozon.getByText("Настройки", { exact: true }).click();
  const moySelector = moy.getByRole("combobox", { name: "МойСклад: аккаунт" });
  const ozonSelector = ozon.getByRole("combobox", { name: "Ozon: аккаунт" });
  expect(configuredMoy).toMatch(/^synthetic-moy-browser-/);
  expect(configuredOzon).toMatch(/^synthetic-ozon-browser-/);
  await moySelector.selectOption(configuredMoy);
  await ozonSelector.selectOption(configuredOzon);
  await expect(moySelector).toHaveValue(configuredMoy);
  await expect(ozonSelector).toHaveValue(configuredOzon);
  await page.locator(".workspace-selector > summary").click();
  await page
    .getByLabel("Рабочее пространство", { exact: true })
    .fill("other-tenant");
  await page.getByRole("button", { name: "Открыть пространство" }).click();
  await expect(
    moy.getByText("Аккаунт недоступен", { exact: true }),
  ).toBeVisible();
  await moy.getByText("Настройки", { exact: true }).click();
  await expect(moy.getByText(/Перенос недоступен/)).toBeVisible();
  await moy.getByRole("link", { name: "Товары", exact: true }).click();
  await expect(page.getByRole("heading", { name: /Products/ })).toBeVisible();
  await page
    .getByRole("navigation")
    .getByRole("link", { name: "Warehouses" })
    .click();
  await expect(page.getByRole("heading", { name: /Warehouses/ })).toBeVisible();
  const observedResponses = (await Promise.all(jsonResponses)).join("\n");
  expect(observedResponses).not.toContain("synthetic-only-key");
  expect(observedResponses).not.toContain("synthetic-only-token");
});
