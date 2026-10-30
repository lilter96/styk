import { test, expect } from "@playwright/test";

test("matching modal uses the local Go search and captures desktop states", async ({
  page,
  context,
}) => {
  test.skip(
    !process.env.STYK_DEMO_TEST_PASSWORD,
    "requires isolated local demo",
  );
  const base = "http://127.0.0.1:5173";
  const login = await context.request.post(base + "/api/session", {
    headers: { Origin: base },
    data: {
      email: "demo@styk.local",
      password: process.env.STYK_DEMO_TEST_PASSWORD,
    },
  });
  expect(login.status()).toBe(204);
  const session = await (
    await context.request.get(base + "/api/session")
  ).json();
  await context.addInitScript(
    (tenant) =>
      sessionStorage.setItem(
        "styk-selected-accounts",
        JSON.stringify({
          tenant,
          moy: "STYK-DEMO-MS",
          ozon: "STYK-DEMO-OZON-1",
        }),
      ),
    session.account_id,
  );
  const query = "moysklad_account=STYK-DEMO-MS&ozon_account=STYK-DEMO-OZON-1";
  const search = async (extra: string) =>
    context.request.get(base + "/api/moysklad-products?" + query + "&" + extra);
  const first = await search("page=1&pageSize=10");
  expect(first.status()).toBe(200);
  const data = await first.json();
  expect(data.total).toBeGreaterThan(10);
  expect(data.items).toHaveLength(10);
  for (const [field, value] of [
    ["name", data.items[0].name],
    ["article", data.items[0].article],
    ["id", data.items[0].id],
    ["code", data.items[0].code],
  ]) {
    if (!value) continue;
    const found = await search(
      `searchBy=${field}&query=${encodeURIComponent(value)}&page=1&pageSize=10`,
    );
    expect(found.status()).toBe(200);
    expect((await found.json()).total).toBeGreaterThan(0);
  }
  const second = await (await search("page=2&pageSize=10")).json();
  expect(second.total).toBe(data.total);
  expect(second.items[0].id).not.toBe(data.items[0].id);
  const stock = await (await search("inStockOnly=true&pageSize=20")).json();
  expect(
    stock.items.every((i: { quantity: string }) => Number(i.quantity) > 0),
  ).toBe(true);
  expect((await search("searchBy=sku")).status()).toBe(422);
  expect((await search("activeOnly=true")).status()).toBe(422);
  expect((await search("pageSize=101")).status()).toBe(400);
  expect(
    (
      await context.request.get(
        base +
          "/api/moysklad-products?" +
          query +
          "&tenant=unrelated-matching-tenant",
      )
    ).status(),
  ).toBe(404);
  await page.setViewportSize({ width: 1920, height: 1080 });
  await page.goto(base + "/products");
  await page.locator(".mapping-select").first().click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toBeVisible();
  if (process.env.STYK_MATCHING_BEFORE) {
    await page.screenshot({
      path: "../docs/audit/product-matching/before.png",
    });
    return;
  }
  await expect(dialog.locator(".matching-result").first()).toBeVisible();
  await page.screenshot({ path: "../docs/audit/product-matching/normal.png" });
  await page.screenshot({
    path: "../docs/audit/product-matching/existing.png",
  });
  await dialog.locator(".matching-result").first().click();
  await expect(dialog.locator(".matching-result.selected")).toHaveCount(1);
  await page.screenshot({
    path: "../docs/audit/product-matching/selected.png",
  });
  const input = dialog.getByRole("textbox", { name: "Поиск товара МойСклад" });
  await input.fill(data.items[0].name);
  await expect(dialog.locator(".matching-result").first()).toBeVisible();
  await expect.poll(() => input.inputValue()).toBe(data.items[0].name);
  await page.screenshot({ path: "../docs/audit/product-matching/search.png" });
  await input.fill("no-such-matching-product-89723");
  await expect(dialog.getByText(/Товары не найдены/)).toBeVisible();
  await page.screenshot({ path: "../docs/audit/product-matching/empty.png" });
  await input.fill("");
  await expect(dialog.locator(".matching-result").first()).toBeVisible();
  for (const [width, height] of [
    [1366, 768],
    [1440, 900],
  ]) {
    await page.setViewportSize({ width, height });
    const box = await dialog.boundingBox();
    expect(box!.width).toBeGreaterThan(width - 100);
    expect(box!.height).toBeLessThanOrEqual(height - 60);
    await expect(
      dialog.getByRole("button", {
        name: "Сопоставить и перейти к следующему",
        exact: true,
      }),
    ).toBeInViewport();
    await page.screenshot({
      path: `../docs/audit/product-matching/${width}.png`,
    });
  }
  await dialog
    .getByRole("button", { name: "Пропустить товар", exact: true })
    .click();
  await expect(dialog.locator(".matching-ozon .mapping-chip")).toHaveText(
    "Не сопоставлен",
  );
  const offer = (
    await dialog
      .locator(".matching-identifier")
      .first()
      .locator("dd")
      .innerText()
  )
    .replace("⧉", "")
    .trim();
  await dialog
    .getByRole("textbox", { name: "Поиск товара МойСклад" })
    .fill(offer.trim());
  await expect(
    dialog.locator(".matching-result").filter({ hasText: offer }).first(),
  ).toBeVisible();
  await dialog
    .locator(".matching-result")
    .filter({ hasText: offer })
    .first()
    .click();
  await dialog
    .getByRole("button", { name: "Проверить соответствие", exact: true })
    .click();
  await expect(
    dialog.getByText("Проверка готова к подтверждению"),
  ).toBeVisible();
  await dialog
    .getByRole("checkbox", { name: "Подтверждаю, что это один и тот же товар" })
    .check();
  await dialog
    .getByRole("button", {
      name: "Сопоставить и перейти к следующему",
      exact: true,
    })
    .click();
  await expect
    .poll(async () =>
      (
        await dialog
          .locator(".matching-identifier")
          .first()
          .locator("dd")
          .innerText()
      )
        .replace("⧉", "")
        .trim(),
    )
    .not.toBe(offer);
  await expect(
    dialog.getByRole("textbox", { name: "Поиск товара МойСклад" }),
  ).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(dialog).not.toBeVisible();
});
