import { test, expect } from "@playwright/test";
import { execFileSync } from "node:child_process";

test("explicit manual variant confirmation survives a lost response and persists an audit", async ({
  page,
}) => {
  let confirmations = 0;
  await page.route(
    "**/api/product-mapping-reviews/*/confirm",
    async (route) => {
      confirmations++;
      if (confirmations === 1) {
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
    .getByRole("link", { name: "Products" })
    .click();
  await page.getByRole("button", { name: "Review", exact: true }).click();
  await page
    .getByLabel("Search by name, article, code or identity")
    .fill("variant");
  await page.getByRole("button", { name: "variant · variant" }).click();
  await expect(page.getByText(/Parent product: parent/)).toBeVisible();
  await page.getByRole("button", { name: "Create review" }).click();
  await expect(page.getByText("Durable review")).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Confirm mapping" }),
  ).toBeDisabled();
  await page.getByLabel("I explicitly confirm this relationship.").check();
  await page.getByRole("button", { name: "Confirm mapping" }).click();
  await expect(page.getByText(/Outcome unknown/)).toBeVisible();
  await page.getByRole("button", { name: "Retry same confirmation" }).click();
  await expect(
    page.getByText(/Existing confirmation recovered by idempotent replay/),
  ).toBeVisible();
  await expect(page.getByText(/Mapping .* version 1/)).toBeVisible();
  const sql =
    "SELECT (SELECT count(*) FROM product_mappings WHERE tenant_id='ui-e2e-tenant' AND target_kind='variant' AND target_id='variant'), (SELECT count(*) FROM product_mapping_confirmation_audits WHERE tenant_id='ui-e2e-tenant' AND evidence_class='OPERATOR_ATTESTED' AND principal_type='LOCAL_OPERATOR_SESSION'), (SELECT count(*) FROM product_mapping_confirmation_idempotency), (SELECT count(*) FROM outbox_tasks)";
  expect(
    execFileSync(
      "docker",
      [
        "exec",
        "styk-live-fixture-test",
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
  ).toBe("1|1|1|0");
  await page.reload();
  await page.getByLabel("Tenant").fill("ui-e2e-tenant");
  await page.getByRole("button", { name: "Open workspace" }).click();
  await page
    .getByRole("combobox", { name: "MoySklad" })
    .selectOption("ui-e2e-moy");
  await page
    .getByRole("combobox", { name: "Ozon" })
    .selectOption("ui-e2e-ozon");
  await expect(page.getByText("Mapped: variant · variant")).toBeVisible();
  await page.getByRole("button", { name: "Review", exact: true }).click();
  await expect(page.getByText(/Mapping .* version 1/)).toBeVisible();
});
