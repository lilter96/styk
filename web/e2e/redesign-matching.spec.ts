import { expect, test } from "@playwright/test";

test("redesigned matching confirms through the current Go API and advances safely", async ({
  page,
  context,
}, testInfo) => {
  test.skip(
    process.env.STYK_REDESIGN_MATCHING_E2E !== "1",
    "requires the isolated Go matching harness",
  );
  const origin = "http://127.0.0.1:15179";
  const scope = new URLSearchParams(process.env.STYK_REDESIGN_MATCHING_SCOPE);
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  await page.route("**/*", (route) =>
    new URL(route.request().url()).hostname === "127.0.0.1"
      ? route.continue()
      : route.abort(),
  );
  expect(
    (await context.request.get(`${origin}/api/products?${scope}`)).status(),
  ).toBe(401);
  expect(
    (
      await context.request.post(`${origin}/api/session`, {
        headers: { Origin: origin },
        data: { fixture: true },
      })
    ).status(),
  ).toBe(204);
  await context.addInitScript(
    (values) =>
      sessionStorage.setItem("styk-selected-accounts", JSON.stringify(values)),
    {
      tenant: scope.get("tenant"),
      moy: scope.get("moysklad_account"),
      ozon: scope.get("ozon_account"),
    },
  );
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto(origin + "/products");
  await expect(
    page.getByRole("checkbox", {
      name: "Выбрать extra-offer-0 для проверки",
      exact: true,
    }),
  ).toBeDisabled();
  await page
    .getByRole("button", {
      name: "Открыть сопоставление extra-offer-0",
      exact: true,
    })
    .click();
  const dialog = page.getByRole("dialog", { name: "Сопоставить товар" });
  const search = dialog.getByRole("textbox", { name: "Поиск товара МойСклад" });
  await expect(search).toBeFocused();
  const searched = page.waitForResponse((response) => {
    const url = new URL(response.url());
    return (
      url.pathname === "/api/moysklad-products" &&
      url.searchParams.get("query") === "Synthetic" &&
      response.status() === 200
    );
  });
  await search.fill("Synthetic");
  await searched;
  await expect(dialog.locator(".matching-result")).toHaveCount(1);
  await dialog
    .getByRole("button", { name: "Synthetic target, товар", exact: true })
    .click();
  await expect(
    dialog.getByRole("button", {
      name: "Сопоставить и перейти к следующему",
      exact: true,
    }),
  ).toBeDisabled();
  await dialog
    .getByRole("button", { name: "Проверить соответствие", exact: true })
    .click();
  await dialog.getByLabel("Подтверждаю, что это один и тот же товар").check();
  await page.screenshot({
    path: testInfo.outputPath("matching-desktop.png"),
    fullPage: true,
  });
  await page.setViewportSize({ width: 390, height: 844 });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  await page.screenshot({
    path: testInfo.outputPath("matching-mobile.png"),
    fullPage: true,
  });
  await dialog
    .getByRole("button", {
      name: "Сопоставить и перейти к следующему",
      exact: true,
    })
    .click();
  await expect(
    dialog.getByText("extra-offer-1", { exact: true }).first(),
  ).toBeVisible();
  await expect(search).toBeFocused();
  const saved = await (
    await context.request.get(
      `${origin}/api/products?${scope}&search=extra-offer-0`,
    )
  ).json();
  expect(saved.total).toBe(1);
  expect(saved.items[0].target_id).toBe("extra");
  expect(saved.items[0].match_status).toBe("CONFIRMED");
  expect(errors).toEqual([]);
});
