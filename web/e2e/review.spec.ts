import { test, expect } from "@playwright/test";

test("review synthetic mapping and audit without writing", async ({ page }) => {
  await page.goto("http://127.0.0.1:5174");
  await page
    .getByLabel("Operator secret")
    .fill("synthetic-review-operator-secret-123456789");
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.getByLabel("Tenant").fill("doctor-synthetic");
  await page.getByRole("button", { name: "Open workspace" }).click();
  await page
    .getByRole("combobox", { name: "MoySklad" })
    .selectOption("doctor-moy");
  await page
    .getByRole("combobox", { name: "Ozon" })
    .selectOption("doctor-ozon");
  await page
    .getByRole("navigation")
    .getByRole("link", { name: "Products" })
    .click();
  await page.getByRole("button", { name: "Review" }).click();
  await expect(
    page.getByRole("heading", { name: "Product review" }),
  ).toBeVisible();
  await page.getByLabel("Article, code or identity").fill("offer");
  await page.getByRole("button", { name: "product · item" }).click();
  await expect(
    page.getByRole("button", { name: "Create review" }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Confirm mapping", exact: true }),
  ).toHaveCount(0);
  await expect(page.getByText(/synthetic explicit confirmation/)).toBeVisible();
});
