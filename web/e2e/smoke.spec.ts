import { test, expect } from "@playwright/test";
import { signIn, selectWorkspace, navigate } from "./workspace";

test("local console reads synthetic observations from the real Go API", async ({
  page,
  request,
}) => {
  const unauthorized = await request.get(
    (process.env.STYK_E2E_API_ORIGIN || "http://127.0.0.1:8080") +
      "/api/registrations?tenant=doctor-synthetic",
  );
  expect(unauthorized.status()).toBe(401);
  await signIn(
    page,
    "synthetic-local-operator-secret-123456789",
    "http://127.0.0.1:5173/",
  );
  await selectWorkspace(page, "doctor-synthetic", "doctor-moy", "doctor-ozon");
  await expect(page.getByText("Данные загружены")).toHaveCount(2);
  await page.screenshot({ path: "test-results/overview.png", fullPage: true });
  await navigate(page, "Товары");
  await expect(
    page.getByRole("cell", { name: "offer", exact: true }),
  ).toBeVisible();
  await expect(page.getByText("Подтверждено")).toBeVisible();
  await navigate(page, "Склады");
  await page
    .locator("section")
    .filter({ has: page.getByRole("heading", { name: "МойСклад" }) })
    .getByRole("button", { name: "Посмотреть остатки" })
    .click();
  await expect(page.getByText(/На складе 10.*В резерве 2/)).toBeVisible();
  await navigate(page, "Обзор");
  await page
    .locator(".read-next")
    .getByRole("link", { name: "Диагностика", exact: true })
    .click();
  await expect(
    page.getByText("Недостаточно данных", { exact: true }).first(),
  ).toBeVisible();
  await expect(
    page.getByText(
      "Диагностика не запускает синхронизацию и не изменяет остатки.",
    ),
  ).toBeVisible();
});
