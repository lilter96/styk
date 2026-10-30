import { test, expect } from "@playwright/test";

test("matching page uses server totals filters pagination and the existing modal", async ({
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
  const get = async (extra: string) =>
    context.request.get(base + "/api/products?" + query + "&" + extra);
  const first = await get("page=1&pageSize=20");
  expect(first.status()).toBe(200);
  const data = await first.json();
  expect(data.total).toBe(500);
  expect(data.items).toHaveLength(20);
  const next = await (await get("page=2&pageSize=20")).json();
  expect(next.total).toBe(data.total);
  expect(next.items[0].product_id).not.toBe(data.items[0].product_id);
  expect((await get("pageSize=101")).status()).toBe(400);
  expect((await get("page=100002")).status()).toBe(400);
  const empty = await (
    await get("search=no-such-local-matching-product-9137&pageSize=20")
  ).json();
  expect(empty.total).toBe(0);
  expect(empty.items).toHaveLength(0);
  const deep = await (await get("page=100&pageSize=20")).json();
  expect(deep.total).toBe(500);
  expect(deep.items).toHaveLength(0);
  const confirmed = await (await get("mapping=confirmed&pageSize=20")).json();
  const unmatched = await (await get("mapping=unconfirmed&pageSize=20")).json();
  expect(confirmed.total + unmatched.total).toBe(data.total);
  const archived = await (await get("archived=true&pageSize=20")).json();
  expect(archived.items.every((p: { archived: boolean }) => p.archived)).toBe(
    true,
  );
  expect(
    (
      await context.request.get(
        base + "/api/products?" + query + "&tenant=other-page-tenant",
      )
    ).status(),
  ).toBe(404);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.setViewportSize({ width: 1536, height: 1024 });
  await page.goto(base + "/products");
  await expect(page.locator(".mapping-select").first()).toBeVisible();
  await expect(
    page.getByRole("progressbar", {
      name: "Сопоставление последнего каталога",
    }),
  ).toBeVisible();
  if (process.env.STYK_PAGE_BEFORE) {
    await page.screenshot({ path: "../docs/audit/matching-page/before.png" });
    return;
  }
  const area = page.locator(".product-mapping-page");
  await expect(area.getByRole("link", { name: /Интеграции/ })).toHaveCount(0);
  await expect(area.locator("thead th")).toHaveText([
    "",
    "Товар в МойСклад",
    "Артикул",
    "Товар в Ozon",
    "Статус",
    "Действия",
  ]);
  await expect(area.locator("tbody tr")).toHaveCount(20);
  await expect(area.locator(".mapping-table-footer")).toContainText(
    "1–20 из 500",
  );
  for (const [width, height] of [
    [1536, 1024],
    [1920, 1080],
    [1440, 900],
    [1366, 768],
  ]) {
    await page.setViewportSize({ width, height });
    await page.screenshot({ path: `../docs/audit/matching-page/${width}.png` });
  }
  await page.setViewportSize({ width: 1536, height: 1024 });
  await context.grantPermissions(["clipboard-read", "clipboard-write"], {
    origin: base,
  });
  await area
    .getByRole("button", { name: /Копировать артикул/ })
    .first()
    .click();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(
    data.items[0].target_article,
  );
  await area
    .getByRole("button", { name: /Изменить соответствие/ })
    .first()
    .click();
  await expect(
    page.getByRole("dialog").getByText("Текущая связь", { exact: true }),
  ).toBeVisible();
  await page.keyboard.press("Escape");
  await area.getByRole("button", { name: "Следующая страница" }).click();
  await expect(area.locator(".mapping-table-footer")).toContainText(
    "21–40 из 500",
  );
  await area
    .getByRole("combobox", { name: "Товаров на странице" })
    .selectOption("100");
  await expect(area.locator("tbody tr")).toHaveCount(100);
  await expect(area.locator(".mapping-table-footer")).toContainText(
    "1–100 из 500",
  );
  const search = area.getByRole("textbox", { name: "Поиск", exact: true });
  await search.fill("no-such-local-matching-product-9137");
  await expect(
    area.getByText("По этим условиям товары не найдены."),
  ).toBeVisible();
  await page.screenshot({ path: "../docs/audit/matching-page/empty.png" });
  await area.getByRole("button", { name: "Сбросить", exact: true }).click();
  await expect(area.locator("tbody tr")).toHaveCount(100);
  await area
    .getByRole("button", { name: "Проверить несопоставленные", exact: true })
    .click();
  await expect(
    area.getByRole("combobox", { name: "Соответствия" }),
  ).toHaveValue("unconfirmed");
  await expect(
    area.getByRole("checkbox", { name: "Выбрать все на странице" }),
  ).toBeEnabled();
  await area.getByRole("checkbox", { name: "Выбрать все на странице" }).check();
  await expect(area.getByText(/Выбрано: [1-9]/)).toBeVisible();
  await page.screenshot({ path: "../docs/audit/matching-page/selected.png" });
  await area
    .getByRole("button", { name: "Сопоставить выбранные", exact: true })
    .click();
  await expect(page.getByRole("dialog")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog")).not.toBeVisible();
  await area
    .getByRole("checkbox", { name: "Выбрать все на странице" })
    .uncheck();
  for (const [width,height] of [[820,1180],[390,844]]) {
    await page.setViewportSize({width,height});
    await expect(area.getByRole("textbox",{name:"Поиск",exact:true})).toBeVisible();
    expect(await page.evaluate(()=>document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
    await page.screenshot({path:`../docs/audit/matching-page/${width}.png`});
  }
  expect(errors).toEqual([]);
});
