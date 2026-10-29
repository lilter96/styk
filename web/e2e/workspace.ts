import type { Page } from "@playwright/test";

export async function signIn(page: Page, secret: string, origin: string) {
  await page.goto(process.env.STYK_E2E_ORIGIN || origin);
  await page
    .getByLabel("Ключ входа")
    .fill(process.env.STYK_E2E_SECRET || secret);
  await page.getByRole("button", { name: "Войти", exact: true }).click();
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
    (scope) => sessionStorage.setItem("styk-selected-accounts", JSON.stringify(scope)),
    { tenant, moy, ozon },
  );
  await page.reload();
  const selector = page.locator(".workspace-selector");
  if ((await selector.getAttribute("open")) === null)
    await selector.locator("summary").first().click();
  await page
    .getByRole("combobox", { name: "МойСклад", exact: true })
    .selectOption(moy);
  await page
    .getByRole("combobox", { name: "Ozon", exact: true })
    .selectOption(ozon);
  if ((await selector.getAttribute("open")) !== null)
    await selector.locator("summary").first().click();
}

export async function navigate(page: Page, name: string) {
  await page
    .locator(".app-tabs")
    .getByRole("link", { name, exact: true })
    .click();
}
