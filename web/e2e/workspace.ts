import { expect, type Locator, type Page } from "@playwright/test";

// Only used by Go integration fixtures; the normal API accepts merchant login.
export async function signIn(page: Page, origin: string) {
  const base = new URL(process.env.STYK_E2E_ORIGIN || origin).origin;
  const response = await page.request.post(base + "/api/session", {
    headers: { Origin: base },
    data: { fixture: true },
  });
  expect(response.status()).toBe(204);
  await page.goto(base);
}

export async function selectWorkspace(
  page: Page,
  tenant: string,
  moy: string,
  ozon: string,
) {
  // Legacy loopback-operator fixtures keep their synthetic scope in test-only
  // browser state; the merchant UI never asks a seller for a tenant ID.
  await page.evaluate(
    (scope) =>
      sessionStorage.setItem("styk-selected-accounts", JSON.stringify(scope)),
    { tenant, moy, ozon },
  );
  await page.reload();
  await page
    .getByRole("combobox", { name: "МойСклад", exact: true })
    .selectOption(moy);
  await page
    .getByRole("combobox", { name: "Ozon", exact: true })
    .selectOption(ozon);
}

export async function navigate(page: Page, name: string) {
  await page
    .locator(".app-tabs")
    .getByRole("link", { name, exact: true })
    .click();
}

// The simulator uses the same explicit evidence form as connected accounts.
export async function reviewStockEvidence(dialog: Locator) {
  await dialog
    .getByRole("button", { name: "Подтвердить условия вручную" })
    .click();
  for (const label of [
    "Ещё не учтённые обязательства, шт.",
    "Запас на ещё не учтённые уменьшения, шт.",
    "Страховой остаток, шт.",
  ])
    await dialog.getByLabel(label, { exact: true }).fill("0");
  const now = new Date();
  const local = new Date(now.getTime() - now.getTimezoneOffset() * 60000)
    .toISOString()
    .slice(0, 16);
  await dialog.getByLabel("На какое время подтверждены условия").fill(local);
  await dialog
    .getByLabel("Основание проверки без ключей доступа")
    .fill("Сверены синтетические обязательства и документы локального стенда");
  const proofs = dialog.locator(".sync-evidence").getByRole("checkbox");
  await expect(proofs).toHaveCount(9);
  for (const proof of await proofs.all()) await proof.check();
  await dialog
    .getByRole("button", { name: "Проверить подтверждённые условия" })
    .click();
}
