import { test, expect } from "@playwright/test";
import { signIn, selectWorkspace, navigate } from "./workspace";
import { execFileSync } from "node:child_process";
test("reviews FBS and rFBS warehouses without creating a mapping", async ({
  page,
}) => {
  await signIn(
    page,
    "synthetic-browser-operator-secret-not-real-123",
    "http://127.0.0.1:15173/",
  );
  await selectWorkspace(page, "ui-e2e-tenant", "ui-e2e-moy", "ui-e2e-ozon");
  await navigate(page, "Склады");
  const ms = page.locator("section").filter({
    has: page.getByRole("heading", { name: "МойСклад", exact: true }),
  });
  await ms.getByRole("button", { name: "Выбрать для сопоставления" }).click();
  const oz = page
    .locator(".warehouse-row")
    .filter({ hasText: "Synthetic FBS" });
  await oz.getByRole("button", { name: "Выбрать для сопоставления" }).click();
  const review = page.getByRole("region", {
    name: "Проверка соответствия складов",
  });
  await expect(review.getByText("Можно проверить соответствие")).toBeVisible();
  await expect(review.getByText(/Тип склада: FBS/)).toBeVisible();
  await review
    .getByText("История соответствий складов", { exact: true })
    .click();
  await expect(
    review.getByText(/Для этих складов доступной истории нет/),
  ).toBeVisible();
  await expect(
    review.getByRole("button", { name: "Проверить пару складов" }),
  ).toBeEnabled();
  await page
    .getByRole("button", { name: "Закрыть сопоставление складов" })
    .click();
  await ms.getByRole("button", { name: "Посмотреть остатки" }).click();
  await expect(ms.getByText(/На складе 0/)).toBeVisible();
  const rfbs = page
    .locator(".warehouse-row")
    .filter({ hasText: "Synthetic rFBS" });
  await ms.getByRole("button", { name: "Выбрать для сопоставления" }).click();
  await rfbs.getByRole("button", { name: "Выбрать для сопоставления" }).click();
  await expect(
    review.getByText("Поддерживаются только склады Ozon FBS"),
  ).toBeVisible();
  await expect(
    review.getByRole("button", { name: "Проверить пару складов" }),
  ).toBeDisabled();
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
