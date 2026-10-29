import { test, expect } from "@playwright/test";
import { signIn, selectWorkspace, navigate } from "./workspace";
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
  await signIn(
    page,
    "synthetic-browser-operator-secret-not-real-123",
    "http://127.0.0.1:15173/",
  );
  await selectWorkspace(page, "ui-e2e-tenant", "ui-e2e-moy", "ui-e2e-ozon");
  await navigate(page, "Склады");
  const ms = page
    .locator(".warehouse-row")
    .filter({ hasText: "Synthetic source warehouse" });
  const oz = page
    .locator(".warehouse-row")
    .filter({ hasText: "Synthetic FBS" });
  await ms.getByRole("button", { name: "Выбрать для сопоставления" }).click();
  await oz.getByRole("button", { name: "Выбрать для сопоставления" }).click();
  const panel = page.getByRole("region", {
    name: "Проверка соответствия складов",
  });
  await expect(panel.getByText("Можно проверить соответствие")).toBeVisible();
  await panel.getByRole("button", { name: "Проверить пару складов" }).click();
  await expect(
    panel.getByText("Проверка складов готова к подтверждению"),
  ).toBeVisible();
  await expect(
    panel.locator(".review-confirmation time[datetime]"),
  ).toHaveCount(2);
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
  await expect(
    panel.getByText(/Проверка устарела либо данные или соответствие/),
  ).toBeVisible({
    timeout: 12000,
  });
  await expect(
    panel.getByRole("button", { name: "Подтвердить соответствие складов" }),
  ).toBeDisabled();
  await panel.getByRole("button", { name: "Проверить пару складов" }).click();
  await expect(
    panel.getByText(/Проверка устарела либо данные или соответствие/),
  ).toHaveCount(0);
  await expect(
    panel.getByRole("button", { name: "Подтвердить соответствие складов" }),
  ).toBeDisabled();
  await panel.getByLabel("Подтверждаю соответствие выбранных складов").check();
  await panel
    .getByRole("button", { name: "Подтвердить соответствие складов" })
    .click();
  await expect(page.getByText(/Результат сохранения уточняется/)).toBeVisible();
  await page.getByRole("button", { name: "Повторить тот же запрос" }).click();
  await expect(page.getByText(/Соответствие.*сохранено/)).toBeVisible();
  await panel
    .getByText("История соответствий складов", { exact: true })
    .click();
  await expect(
    panel.locator(".mapping-history").getByText(/Соответствие .*версия 1/),
  ).toBeVisible();
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
  await selectWorkspace(page, "ui-e2e-tenant", "ui-e2e-moy", "ui-e2e-ozon");
  await expect(
    page.getByText("Соответствие подтверждено").first(),
  ).toBeVisible();
  await page
    .locator(".warehouse-row")
    .filter({ hasText: "Synthetic source warehouse" })
    .getByRole("button", { name: "Выбрать для сопоставления" })
    .click();
  await page
    .locator(".warehouse-row")
    .filter({ hasText: "Synthetic FBS" })
    .getByRole("button", { name: "Выбрать для сопоставления" })
    .click();
  await expect(panel.getByText("Склады уже сопоставлены")).toBeVisible();
  await expect(
    panel.getByRole("button", { name: "Проверить пару складов" }),
  ).toBeDisabled();
  await panel
    .getByText("История соответствий складов", { exact: true })
    .click();
  await expect(
    panel.locator(".mapping-history").getByText(/Соответствие .*версия 1/),
  ).toBeVisible();
  await page
    .getByRole("button", { name: "Закрыть сопоставление складов" })
    .click();
  await ms.getByRole("button", { name: "Выбрать для сопоставления" }).click();
  await page
    .locator(".warehouse-row")
    .filter({ hasText: "Synthetic rFBS" })
    .getByRole("button", { name: "Выбрать для сопоставления" })
    .click();
  await expect(panel.getByText("Есть конфликт соответствий")).toBeVisible();
  await expect(
    panel.getByRole("button", { name: "Проверить пару складов" }),
  ).toBeDisabled();
  expect(
    psql(
      "SELECT count(*) FROM warehouse_mapping_confirmation_audits WHERE tenant_id='ui-e2e-tenant'",
    ),
  ).toBe("1");
});
