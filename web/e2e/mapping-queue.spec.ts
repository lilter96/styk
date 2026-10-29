import { expect, test } from "@playwright/test";

for (const [label, width, height] of [
  ["mobile", 390, 844],
  ["desktop", 1440, 900],
] as const) {
  test(`${label}: page selection creates a review queue without confirming products`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height });
    await page.addInitScript(() => {
      sessionStorage.setItem(
        "styk-selected-accounts",
        JSON.stringify({ tenant: "synthetic", moy: "moy", ozon: "ozon" }),
      );
    });
    let writes = 0;
    const products = ["one", "two"].map((offer_id, index) => ({
      product_id: `product-${index}`,
      offer_id,
      name: `Товар ${index}`,
      sku: `sku-${index}`,
      archived: false,
      target_kind: null,
      target_id: null,
      match_status: "ONE_CANDIDATE",
      candidate_count: 1,
      candidates: [
        {
          kind: "product",
          id: `target-${index}`,
          name: `Предложение ${index}`,
          evidence: ["article"],
        },
      ],
      candidates_truncated: false,
    }));
    await page.route("**/api/**", async (route) => {
      const url = new URL(route.request().url());
      if (route.request().method() !== "GET") writes++;
      let body: unknown = {};
      if (url.pathname === "/api/session") body = null;
      else if (url.pathname === "/api/registrations")
        body = {
          accounts: [
            { source: "moysklad", account: "moy" },
            { source: "ozon", account: "ozon" },
          ],
          truncated: false,
        };
      else if (url.pathname === "/api/products")
        body = { items: products, offset: 0, has_next: false };
      else if (url.pathname === "/api/moysklad-products")
        body = { items: [], offset: 0, has_next: false };
      else if (url.pathname === "/api/diagnostics")
        body = { mapping_counts: {} };
      else if (url.pathname === "/api/product-mapping-history")
        body = { items: [], offset: 0, has_next: false };
      await route.fulfill({ json: body });
    });
    await page.goto("http://127.0.0.1:5173/products");
    await expect(page.getByText("Товар 0", { exact: true })).toBeVisible();
    await expect(page.getByText("Offer ID: one")).toBeVisible();
    await page
      .getByRole("button", { name: "Выбрать предложения на странице" })
      .click();
    await page.getByText("Выбрано для проверки: 2").click();
    await expect(
      page.getByText("Подтверждено: 0 · Осталось проверить: 2"),
    ).toBeVisible();
    if (process.env.STYK_QUEUE_SCREENSHOTS) {
      await page.screenshot({
        path: `/tmp/styk-queue-${label}.png`,
        fullPage: true,
      });
    }
    await page.getByRole("button", { name: "Проверить one" }).click();
    await expect(
      page.getByRole("dialog", { name: "Сопоставить товар" }),
    ).toBeVisible();
    await expect(page.getByText("ID товара: product-0").last()).toBeVisible();
    expect(writes).toBe(0);
  });
}
