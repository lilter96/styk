import { expect, test, type BrowserContext } from "@playwright/test";

const base = "http://127.0.0.1:5173";
const sizes = [
  [1920, 1080],
  [1536, 1024],
  [1366, 768],
  [390, 844],
] as const;
let sessionCookies: Awaited<ReturnType<BrowserContext["cookies"]>> = [];
test.beforeAll(async ({ browser }) => {
  if (!process.env.STYK_DEMO_TEST_PASSWORD) return;
  const context = await browser.newContext();
  const response = await context.request.post(base + "/api/session", {
    headers: { Origin: base },
    data: {
      email: "demo@styk.local",
      password: process.env.STYK_DEMO_TEST_PASSWORD,
    },
  });
  expect(response.status()).toBe(204);
  sessionCookies = await context.cookies();
  await context.close();
});

for (const [width, height] of sizes) {
  test(`brand system and authenticated settings at ${width}`, async ({
    page,
    context,
  }) => {
    test.skip(
      !process.env.STYK_DEMO_TEST_PASSWORD,
      "requires the local demo account",
    );
    const errors: string[] = [];
    page.on("pageerror", (error) => errors.push(error.message));
    page.on("console", (message) => {
      if (message.type() === "error") errors.push(message.text());
    });
    page.on("response", (response) => {
      if (response.url().includes("/assets/") && response.status() >= 400)
        errors.push(`asset HTTP ${response.status()}`);
    });
    await page.setViewportSize({ width, height });
    await context.addCookies(sessionCookies);
    expect((await context.request.get(base + "/api/session")).status()).toBe(
      200,
    );
    await page.goto(base + "/integrations");
    await expect(
      page.locator(".integration-provider-header .brand-logo"),
    ).toHaveCount(4);
    await page.waitForLoadState("networkidle");
    const logos = page.locator(".integration-provider-header .brand-logo");
    expect(
      await logos.evaluateAll((nodes) =>
        nodes.map((node) => {
          const rect = node.getBoundingClientRect(),
            css = getComputedStyle(node);
          return [
            rect.width,
            rect.height,
            css.borderRadius,
            css.borderTopWidth,
            css.flexShrink,
          ];
        }),
      ),
    ).toEqual(Array.from({ length: 4 }, () => [36, 36, "9px", "1px", "0"]));
    for (const image of await page.locator(".brand-logo img").all()) {
      await expect
        .poll(() =>
          image.evaluate(
            (node: HTMLImageElement) => node.complete && node.naturalWidth > 0,
          ),
        )
        .toBe(true);
      expect(
        await image.evaluate((node) => getComputedStyle(node).objectFit),
      ).toBe("contain");
    }
    await expect(
      page.locator(".integration-account-card .brand-logo"),
    ).toHaveCount(0);
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBe(true);
    await page.screenshot({
      path: `../docs/audit/brand-system/after/integrations-${width}.png`,
      fullPage: true,
    });
    const ozon = page.locator('[data-provider-group="ozon"]');
    const next = ozon.getByRole("button", { name: "Ozon: следующая страница" });
    if (await next.count()) {
      await next.click();
      await expect
        .poll(() =>
          ozon
            .locator(".integration-account-carousel")
            .evaluate((node) => node.scrollLeft),
        )
        .toBeGreaterThan(0);
      await ozon
        .getByRole("button", { name: "Ozon: предыдущая страница" })
        .click();
      await expect
        .poll(() =>
          ozon
            .locator(".integration-account-carousel")
            .evaluate((node) => node.scrollLeft),
        )
        .toBe(0);
    }
    await ozon
      .getByRole("button", { name: "Настройки", exact: true })
      .first()
      .click();
    const dialog = page.getByRole("dialog", { name: "Подключение Ozon" });
    await expect(dialog).toBeVisible();
    await expect(
      dialog.locator('.brand-logo[data-brand="ozon"]'),
    ).toBeVisible();
    expect(
      await dialog.evaluate(
        (node) => node.getBoundingClientRect().right <= innerWidth,
      ),
    ).toBe(true);
    await page.screenshot({
      path: `../docs/audit/brand-system/after/ozon-settings-${width}.png`,
      fullPage: true,
    });
    await page.keyboard.press("Escape");
    await expect(dialog).toHaveCount(0);
    await page
      .locator('[data-provider-group="moysklad"]')
      .getByRole("button", { name: "Настройки", exact: true })
      .first()
      .click();
    await expect(page).toHaveURL(
      /\/integrations\/moysklad\/settings\?account=/,
    );
    await expect(page.locator(".moy-summary-brand .brand-logo")).toBeVisible();
    await page.waitForLoadState("networkidle");
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBe(true);
    await page.screenshot({
      path: `../docs/audit/brand-system/after/moysklad-settings-${width}.png`,
      fullPage: true,
    });
    if (width === 390)
      await page.getByRole("button", { name: "Открыть меню" }).click();
    const workspace = page
      .locator(".workspace-switcher")
      .filter({ visible: true });
    await workspace
      .getByRole("button", { name: "Wildberries", exact: true })
      .click();
    await expect(
      workspace.getByRole("combobox", { name: "Wildberries", exact: true }),
    ).toBeDisabled();
    await workspace.getByRole("button", { name: "Ozon", exact: true }).click();
    const account = workspace.getByRole("combobox", {
      name: "Ozon",
      exact: true,
    });
    await account.selectOption("STYK-DEMO-OZON-2");
    await expect(account).toHaveValue("STYK-DEMO-OZON-2");
    await account.focus();
    await page.keyboard.press("Shift+Tab");
    await page.keyboard.press("Shift+Tab");
    await expect(
      workspace.getByRole("button", { name: "Ozon", exact: true }),
    ).toBeFocused();
    expect(
      await workspace
        .getByRole("button", { name: "Ozon", exact: true })
        .evaluate((node) => getComputedStyle(node).outlineStyle),
    ).toBe("solid");
    expect(errors).toEqual([]);
  });
}
