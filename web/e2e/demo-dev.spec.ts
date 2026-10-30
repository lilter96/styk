import { expect, test } from "@playwright/test";

test("isolated demo supports protected reads and all working pages", async ({ page, context }) => {
  const password = process.env.STYK_DEMO_TEST_PASSWORD;
  test.skip(!password, "requires the local DPAPI demo lifecycle helper");
  const base = "http://127.0.0.1:5173";
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  page.on("console", (message) => {
    if (message.type() === "error") errors.push(message.text());
  });
  const login = await context.request.post(base + "/api/session", {
    headers: { Origin: base },
    data: { email: "demo@styk.local", password },
  });
  expect(login.status()).toBe(204);
  const cookie = (await context.cookies()).find((item) => item.name === "styk_session");
  expect(cookie?.httpOnly).toBe(true);
  expect(cookie?.sameSite).toBe("Strict");
  const sessionResponse = await context.request.get(base + "/api/session");
  expect(sessionResponse.status()).toBe(200);
  const session = await sessionResponse.json();
  await context.addInitScript((tenant) => {
    sessionStorage.setItem("styk-selected-accounts", JSON.stringify({ tenant, moy: "STYK-DEMO-MS", ozon: "STYK-DEMO-OZON-1" }));
  }, session.account_id);
  const query = "?moysklad_account=STYK-DEMO-MS&ozon_account=STYK-DEMO-OZON-1";
  for (const path of ["/api/products" + query, "/api/warehouses?source=moysklad&account=STYK-DEMO-MS", "/api/stock?source=moysklad&account=STYK-DEMO-MS&warehouse=DEMO-MS-WH-1", "/api/integrations", "/api/imports?ozon_account=STYK-DEMO-OZON-1", "/api/diagnostics" + query]) {
    const response = await context.request.get(base + path);
    expect(response.status(), path).toBe(200);
  }
  const products = await (await context.request.get(base + "/api/products" + query)).json();
  expect(products.items.length).toBeGreaterThan(0);
  const foreign = await context.request.get(base + "/api/integrations?tenant=unrelated-demo-tenant");
  expect(foreign.status()).toBe(404);
  const csrf = await context.request.post(base + "/api/catalog-sync/config", { headers: { Origin: base }, data: {} });
  expect(csrf.status()).toBe(403);
  for (const route of ["/", "/products", "/warehouses", "/integrations", "/catalog-sync", "/diagnostics"]) {
    await page.goto(base + route);
    await expect(page.locator(".app-sidebar")).toBeVisible();
    await expect(page.locator("#workspace-content")).toBeVisible();
    await page.waitForLoadState("networkidle");
  }
  expect(errors).toEqual([]);
});

test("anonymous access and retired login remain closed", async ({ request }) => {
  test.skip(!process.env.STYK_DEMO_TEST_PASSWORD, "requires local demo");
  const base = "http://127.0.0.1:5173";
  const anonymous = await request.get(base + "/api/integrations");
  expect(anonymous.status()).toBe(401);
  const retired = await request.post(base + "/api/session", { headers: { Origin: base }, data: { secret: "retired-mechanism" } });
  expect(retired.status()).toBe(400);
});
