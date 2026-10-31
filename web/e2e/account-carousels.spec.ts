import { expect, test } from "@playwright/test";
const origin = process.env.STYK_CAROUSEL_ORIGIN;
test("independent account carousels support boundaries, search, resize, keyboard and changing lists", async ({ page }) => {
  test.skip(!origin, "explicit local fixture origin required");
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto(origin + "/e2e/fixtures/account-carousels.html");
  const a = page.locator("[data-provider-group=ozon]"), b = page.locator("[data-provider-group=moysklad]");
  const track = a.locator(".integration-account-carousel");
  const next = a.getByRole("button", { name: "Ozon: следующая страница" }), prev = a.getByRole("button", { name: "Ozon: предыдущая страница" });
  for (const count of [1,3,5,10,20]) {
    await page.getByLabel("Fixture account count").selectOption(String(count));
    await expect(a.locator("article")).toHaveCount(count);
    await expect.poll(() => track.evaluate((node) => node.scrollLeft)).toBe(0);
    if (count <= 4) { await expect(a.locator(".provider-carousel-controls")).toHaveCount(0); continue; }
    await expect(prev).toBeDisabled();
    await next.click();
    await expect.poll(() => track.evaluate((node) => node.scrollLeft)).toBeGreaterThan(0);
    expect(await b.locator(".integration-account-carousel").evaluate((node) => node.scrollLeft)).toBe(0);
    await track.focus(); await page.keyboard.press("End"); await expect(next).toBeDisabled();
    await page.keyboard.press("Home"); await expect(prev).toBeDisabled();
  }
  await a.getByRole("searchbox").fill("Account 20");
  await expect(a.locator("article")).toHaveCount(1); await expect(a.locator(".provider-carousel-controls")).toHaveCount(0);
  await a.getByRole("searchbox").fill("not found"); await expect(a.getByText("Аккаунтов с таким названием не найдено.")).toBeVisible();
  await a.getByRole("searchbox").fill(""); await expect(a.locator("article")).toHaveCount(20);
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(a.locator("output")).toHaveText("1 / 20");
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await next.click(); await expect(a.locator("output")).toHaveText("2 / 20");
  await page.screenshot({ path: "../.local/integrations-v3/carousel-20-mobile.png", fullPage: true });
});
