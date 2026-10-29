import { test, expect } from "@playwright/test";
import { execFileSync } from "node:child_process";

test("explicit warehouse confirmation survives lost response and persists audit", async ({
  page,
}) => {
  let requests = 0;
  await page.route(
    "**/api/warehouse-mapping-reviews/*/confirm",
    async (route) => {
      requests++;
      if (requests === 1) {
        await route.fetch();
        await route.abort("failed");
      } else await route.continue();
    },
  );
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
    .locator(".warehouse-row")
    .filter({ hasText: "Synthetic source warehouse" });
  const oz = page
    .locator(".warehouse-row")
    .filter({ hasText: "Synthetic FBS" });
  await ms.getByRole("button", { name: "Select for mapping review" }).click();
  await oz.getByRole("button", { name: "Select for mapping review" }).click();
  const panel = page.getByRole("region", { name: "Warehouse mapping review" });
  await expect(panel.getByText("ELIGIBLE_FOR_REVIEW")).toBeVisible();
  await panel.getByRole("button", { name: "Create warehouse review" }).click();
  await expect(panel.getByText("Durable warehouse review")).toBeVisible();
  await expect(panel.locator("time[datetime]")).toHaveCount(2);
  const container =
    process.env.STYK_E2E_PG_CONTAINER || "styk-live-fixture-test";
  const psql = (sql: string) =>
    execFileSync(
      "docker",
      [
        "exec",
        container,
        "psql",
        "-U",
        "postgres",
        "-d",
        "styk_mapping_ui_e2e_v1",
        "-Atc",
        sql,
      ],
      { encoding: "utf8" },
    ).trim();
  expect(
    psql(
      "SELECT count(*) FROM warehouse_mappings WHERE tenant_id='ui-e2e-tenant'",
    ),
  ).toBe("0");
  psql(
    "UPDATE source_imports SET fingerprint='synthetic-stale-fingerprint' WHERE source='ozon' AND account_id='ui-e2e-ozon'",
  );
  await expect(panel.getByText(/Status: STALE_SOURCE/)).toBeVisible({
    timeout: 12000,
  });
  await expect(
    panel.getByRole("button", { name: "Confirm warehouse mapping" }),
  ).toBeDisabled();
  await panel.getByRole("button", { name: "Create warehouse review" }).click();
  await expect(panel.getByText(/Status: CURRENT/)).toBeVisible();
  await expect(
    panel.getByRole("button", { name: "Confirm warehouse mapping" }),
  ).toBeDisabled();
  await panel
    .getByLabel("I explicitly confirm this warehouse relationship.")
    .check();
  await panel
    .getByRole("button", { name: "Confirm warehouse mapping" })
    .click();
  await expect(page.getByText(/Outcome unknown/)).toBeVisible();
  await page.getByRole("button", { name: "Retry same confirmation" }).click();
  await expect(
    page.getByText(/Existing confirmation recovered by idempotent replay/),
  ).toBeVisible();
  await expect(panel.getByText(/Mapping .* version 1/).first()).toBeVisible();
  const sql =
    "SELECT (SELECT count(*) FROM warehouse_mappings WHERE tenant_id='ui-e2e-tenant'),(SELECT count(*) FROM warehouse_mapping_history WHERE tenant_id='ui-e2e-tenant'),(SELECT count(*) FROM warehouse_mapping_confirmation_audits WHERE tenant_id='ui-e2e-tenant' AND evidence_class='OPERATOR_ATTESTED' AND principal_type='LOCAL_OPERATOR_SESSION'),(SELECT count(*) FROM warehouse_mapping_confirmation_idempotency),(SELECT count(*) FROM stock_pools),(SELECT count(*) FROM stock_decisions),(SELECT count(*) FROM outbox_tasks)";
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
  ).toBe("1|1|1|1|0|0|0");
  expect(() =>
    execFileSync(
      "docker",
      [
        "exec",
        container,
        "psql",
        "-v",
        "ON_ERROR_STOP=1",
        "-U",
        "postgres",
        "-d",
        "styk_mapping_ui_e2e_v1",
        "-c",
        "UPDATE warehouse_mapping_confirmation_audits SET explanation='changed' WHERE tenant_id='ui-e2e-tenant'",
      ],
      { encoding: "utf8", stdio: "pipe" },
    ),
  ).toThrow();
  await page.reload();
  await page.getByLabel("Tenant").fill("ui-e2e-tenant");
  await page.getByRole("button", { name: "Open workspace" }).click();
  await page
    .getByRole("combobox", { name: "MoySklad" })
    .selectOption("ui-e2e-moy");
  await page
    .getByRole("combobox", { name: "Ozon" })
    .selectOption("ui-e2e-ozon");
  await expect(page.getByText("Confirmed mapping").first()).toBeVisible();
  await page
    .locator(".warehouse-row")
    .filter({ hasText: "Synthetic source warehouse" })
    .getByRole("button", { name: "Select for mapping review" })
    .click();
  await page
    .locator(".warehouse-row")
    .filter({ hasText: "Synthetic FBS" })
    .getByRole("button", { name: "Select for mapping review" })
    .click();
  await expect(panel.getByText("ALREADY_MAPPED")).toBeVisible();
  await expect(
    panel.getByRole("button", { name: "Create warehouse review" }),
  ).toBeDisabled();
  await expect(panel.getByText(/Mapping .*version 1/).first()).toBeVisible();
  await page
    .locator(".warehouse-row")
    .filter({ hasText: "Synthetic rFBS" })
    .getByRole("button", { name: "Select for mapping review" })
    .click();
  await expect(panel.getByText("MAPPING_CONFLICT")).toBeVisible();
  await expect(
    panel.getByRole("button", { name: "Create warehouse review" }),
  ).toBeDisabled();
  expect(
    psql(
      "SELECT count(*) FROM warehouse_mapping_confirmation_audits WHERE tenant_id='ui-e2e-tenant'",
    ),
  ).toBe("1");
});
