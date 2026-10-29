import { test, expect } from "@playwright/test";
import { execFileSync } from "node:child_process";
test("reviews FBS and rFBS warehouses without creating a mapping", async ({
  page,
}) => {
  await page.goto("http://127.0.0.1:15173/");
  await page
    .getByLabel("Operator secret")
    .fill("synthetic-browser-operator-secret-not-real-123");
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.getByLabel("Tenant").fill("ui-e2e-tenant");
  await page.getByRole("button", { name: "Open workspace" }).click();
  await page
    .getByRole("combobox", { name: "MoySklad" })
    .selectOption("ui-e2e-moy");
  await page
    .getByRole("combobox", { name: "Ozon" })
    .selectOption("ui-e2e-ozon");
  await page
    .getByRole("navigation")
    .getByRole("link", { name: "Warehouses" })
    .click();
  const ms = page
    .locator("section")
    .filter({
      has: page.getByRole("heading", { name: "MoySklad", exact: true }),
    });
  await ms.getByRole("button", { name: "Select for mapping review" }).click();
  const oz = page
    .locator(".warehouse-row")
    .filter({ hasText: "Synthetic FBS" });
  await oz.getByRole("button", { name: "Select for mapping review" }).click();
  const review = page.getByRole("region", { name: "Warehouse mapping review" });
  await expect(review.getByText("ELIGIBLE_FOR_REVIEW")).toBeVisible();
  await expect(review.getByText(/Classification: FBS/)).toBeVisible();
  await expect(review.getByText(/No mapping history/)).toBeVisible();
  await expect(review.getByText(/Confirmation is unavailable/)).toBeVisible();
  await ms.getByRole("button", { name: "View stock" }).click();
  await expect(ms.getByText(/Physical 0/)).toBeVisible();
  const rfbs = page
    .locator(".warehouse-row")
    .filter({ hasText: "Synthetic rFBS" });
  await rfbs.getByRole("button", { name: "Select for mapping review" }).click();
  await expect(review.getByText("UNSUPPORTED_WAREHOUSE")).toBeVisible();
  const sql =
    "SELECT (SELECT count(*) FROM warehouse_mappings WHERE tenant_id='ui-e2e-tenant'),(SELECT count(*) FROM warehouse_mapping_history WHERE tenant_id='ui-e2e-tenant'),(SELECT count(*) FROM outbox_tasks)";
  expect(
    execFileSync(
      "docker",
      [
        "exec",
        process.env.STYK_E2E_PG_CONTAINER || "styk-live-fixture-test",
        "psql",
        "-U",
        "postgres",
        "-d",
        "styk_mapping_ui_e2e_v1",
        "-Atc",
        sql,
      ],
      { encoding: "utf8" },
    ).trim(),
  ).toBe("0|0|0");
});
