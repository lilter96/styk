import { test, expect } from "@playwright/test";

test("local console reads synthetic observations from the real Go API", async ({
  page,
  request,
}) => {
  const unauthorized = await request.get(
    "http://127.0.0.1:8080/api/registrations?tenant=doctor-synthetic",
  );
  expect(unauthorized.status()).toBe(401);
  await page.goto("http://127.0.0.1:5173/");
  await page
    .getByLabel("Operator secret")
    .fill("synthetic-local-operator-secret-123456789");
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.getByLabel("Tenant").fill("doctor-synthetic");
  await page.getByRole("button", { name: "Open workspace" }).click();
  await page
    .getByRole("combobox", { name: "MoySklad" })
    .selectOption("doctor-moy");
  await page
    .getByRole("combobox", { name: "Ozon" })
    .selectOption("doctor-ozon");
  await expect(page.getByText("Completed import")).toHaveCount(2);
  await page.screenshot({ path: "test-results/overview.png", fullPage: true });
  await page
    .getByRole("navigation")
    .getByRole("link", { name: "Products" })
    .click();
  await expect(page.getByRole("cell", { name: "offer" })).toBeVisible();
  await expect(page.getByText("Mapped: product · item")).toBeVisible();
  await page
    .getByRole("navigation")
    .getByRole("link", { name: "Warehouses" })
    .click();
  await page
    .locator("section")
    .filter({ has: page.getByRole("heading", { name: "MoySklad" }) })
    .getByRole("button", { name: "View stock" })
    .click();
  await expect(page.getByText(/Physical 10.*Reserved 2/)).toBeVisible();
  await page
    .getByRole("navigation")
    .getByRole("link", { name: "Diagnostics" })
    .click();
  await expect(
    page.getByText("unknown", { exact: true }).first(),
  ).toBeVisible();
  await expect(page.getByText("Publication disabled")).toBeVisible();
});
