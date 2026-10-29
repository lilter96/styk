import { test, expect } from "@playwright/test";
import { signIn, selectWorkspace, navigate } from "./workspace";

test("review synthetic mapping and audit without writing", async ({ page }) => {
  await signIn(
    page,
    "synthetic-review-operator-secret-123456789",
    "http://127.0.0.1:5174",
  );
  await selectWorkspace(page, "doctor-synthetic", "doctor-moy", "doctor-ozon");
  await navigate(page, "Товары");
  await page.getByRole("button", { name: /^Сопоставить / }).click();
  await expect(
    page.getByRole("heading", { name: "Сопоставить товар" }),
  ).toBeVisible();
  await page.getByLabel("Поиск товара МойСклад").fill("offer");
  await page.getByRole("button", { name: "Без названия, товар" }).click();
  await expect(
    page.getByRole("button", { name: "Проверить соответствие" }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Подтвердить соответствие", exact: true }),
  ).toHaveCount(0);
  await page.getByText("История соответствий", { exact: true }).click();
  await page
    .locator(".mapping-history")
    .getByText("Подробнее", { exact: true })
    .click();
  await expect(page.getByText(/synthetic explicit confirmation/)).toBeVisible();
});
