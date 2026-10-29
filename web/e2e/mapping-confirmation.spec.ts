import { test, expect } from "@playwright/test";
import { signIn, selectWorkspace, navigate } from "./workspace";
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
  await signIn(
    page,
    "synthetic-browser-operator-secret-not-real-123",
    "http://127.0.0.1:15173/",
  );
  await selectWorkspace(page, "ui-e2e-tenant", "ui-e2e-moy", "ui-e2e-ozon");
  await navigate(page, "Товары");
  await page.getByRole("button", { name: /^Сопоставить / }).click();
  await page.getByLabel("Поиск товара МойСклад").fill("variant");
  await page.getByRole("button", { name: "Без названия, вариант" }).click();
  await page.getByText("Технические данные товаров", { exact: true }).click();
  await expect(
    page.getByText("Родительский товар", { exact: true }),
  ).toBeVisible();
  await expect(
    page.locator("code").getByText("parent", { exact: true }),
  ).toBeVisible();
  await page.getByText("Технические данные товаров", { exact: true }).click();
  await page.getByRole("button", { name: "Проверить соответствие" }).click();
  await expect(page.getByText("Проверка готова к подтверждению")).toBeVisible();
  await expect(page.locator(".review-confirmation time[datetime]")).toHaveCount(
    2,
  );
  await expect(
    page.getByRole("button", { name: "Подтвердить соответствие" }),
  ).toBeDisabled();
  await page.getByLabel("Подтверждаю, что это один и тот же товар").check();
  await page.getByRole("button", { name: "Подтвердить соответствие" }).click();
  await expect(page.getByText(/Результат сохранения уточняется/)).toBeVisible();
  await page.getByRole("button", { name: "Повторить тот же запрос" }).click();
  await expect(page.getByText(/Соответствие.*сохранено/)).toBeVisible();
  await page.getByText("История соответствий", { exact: true }).click();
  await expect(
    page.locator(".mapping-history").getByText(/Соответствие .*версия 1/),
  ).toBeVisible();
  const sql =
    "SELECT (SELECT count(*) FROM product_mappings WHERE tenant_id='ui-e2e-tenant' AND target_kind='variant' AND target_id='variant'), (SELECT count(*) FROM product_mapping_confirmation_audits WHERE tenant_id='ui-e2e-tenant' AND evidence_class='OPERATOR_ATTESTED' AND principal_type='LOCAL_OPERATOR_SESSION'), (SELECT count(*) FROM product_mapping_confirmation_idempotency), (SELECT count(*) FROM outbox_tasks)";
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
  ).toBe("1|1|1|0");
  await page.reload();
  await selectWorkspace(page, "ui-e2e-tenant", "ui-e2e-moy", "ui-e2e-ozon");
  await expect(page.getByText("Подтверждено", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: /^Сопоставить / }).click();
  await page.getByText("История соответствий", { exact: true }).click();
  await expect(
    page.locator(".mapping-history").getByText(/Соответствие .*версия 1/),
  ).toBeVisible();
});
