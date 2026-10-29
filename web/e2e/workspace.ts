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
  const selector = page.locator(".workspace-selector");
  if ((await selector.getAttribute("open")) === null)
    await selector.locator("summary").first().click();
  await page.getByLabel("Рабочее пространство", { exact: true }).fill(tenant);
  await page.getByRole("button", { name: "Открыть пространство" }).click();
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
