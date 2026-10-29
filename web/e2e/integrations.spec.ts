import { expect, test } from "@playwright/test";

test("explicit local registration uses bounded synthetic providers", async ({
  page,
  request,
}) => {
  const api = process.env.STYK_E2E_API_URL!;
  const origin = "http://127.0.0.1:15174";
  const jsonResponses: Promise<string>[] = [];
  page.on("response", (response) => {
    if (
      response.url().includes("/api/") &&
      (response.headers()["content-type"] || "").includes("application/json")
    ) {
      jsonResponses.push(response.text().catch(() => ""));
    }
  });
  const body = { tenant: "browser-tenant", acknowledge: true };
  expect(
    (
      await request.post(`${api}/api/integrations/moysklad/register`, {
        headers: { Origin: origin },
        data: body,
      })
    ).status(),
  ).toBe(401);
  await page.goto(origin);
  await page
    .getByLabel("Operator secret")
    .fill("ssssssssssssssssssssssssssssssss");
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.getByLabel("Tenant", { exact: true }).fill("browser-tenant");
  await page.getByRole("button", { name: "Open workspace" }).click();
  await page
    .getByRole("navigation")
    .getByRole("link", { name: "Integrations" })
    .click();
  const moy = page
    .locator("article")
    .filter({ has: page.getByRole("heading", { name: "MoySklad" }) });
  await expect(moy.getByText("CONFIGURED_NOT_VERIFIED")).toBeVisible();
  await expect(
    moy.getByText(/Configured account ID: synthetic-moy-browser-/),
  ).toBeVisible();
  await expect(
    moy.getByRole("button", { name: "Register account" }),
  ).toBeDisabled();
  const response = await page.request.get(
    `${api}/api/integrations?tenant=browser-tenant`,
  );
  const status = await response.text();
  expect(status).not.toContain("synthetic-only-token");
  expect(status).not.toContain("synthetic-only-key");
  const providers = JSON.parse(status).providers as {
    provider: string;
    account: string;
  }[];
  const configuredMoy = providers.find(
    (p) => p.provider === "moysklad",
  )!.account;
  const configuredOzon = providers.find((p) => p.provider === "ozon")!.account;
  await moy.getByRole("button", { name: "Test connection" }).click();
  await expect(moy.getByText("VERIFIED")).toBeVisible();
  await expect(moy.getByText(/a returned product matched/)).toBeVisible();
  let attempts = 0;
  await page.route("**/api/integrations/moysklad/register", async (route) => {
    attempts++;
    await route.fetch();
    await route.abort("failed");
  });
  await moy.getByLabel(/I attest/).check();
  await moy.getByRole("button", { name: "Register account" }).click();
  await expect(page.getByText(/Registration outcome is unknown/)).toBeVisible();
  await expect(
    moy.getByRole("button", { name: "Register account" }),
  ).toBeDisabled();
  await page
    .getByRole("button", { name: "Check registration outcome" })
    .click();
  await expect(moy.getByText("Configured account: REGISTERED")).toBeVisible();
  expect(attempts).toBe(1);
  const ozon = page
    .locator("article")
    .filter({ has: page.getByRole("heading", { name: "Ozon" }) });
  await ozon.getByRole("button", { name: "Test connection" }).click();
  await expect(ozon.getByText("VERIFIED")).toBeVisible();
  await expect(ozon.getByText(/not independently established/)).toBeVisible();
  await ozon.getByLabel(/I attest/).check();
  await ozon.getByRole("button", { name: "Register account" }).click();
  await expect(ozon.getByText("Configured account: REGISTERED")).toBeVisible();
  await page.reload();
  await page.getByLabel("Tenant", { exact: true }).fill("browser-tenant");
  await page.getByRole("button", { name: "Open workspace" }).click();
  await expect(moy.getByText("Configured account: REGISTERED")).toBeVisible();
  await expect(ozon.getByText("Configured account: REGISTERED")).toBeVisible();
  const moySelector = page.getByRole("combobox", { name: "MoySklad" });
  const ozonSelector = page.getByRole("combobox", { name: "Ozon" });
  expect(configuredMoy).toMatch(/^synthetic-moy-browser-/);
  expect(configuredOzon).toMatch(/^synthetic-ozon-browser-/);
  await moySelector.selectOption(configuredMoy);
  await ozonSelector.selectOption(configuredOzon);
  await expect(moySelector).toHaveValue(configuredMoy);
  await expect(ozonSelector).toHaveValue(configuredOzon);
  await page.getByLabel("Tenant", { exact: true }).fill("other-tenant");
  await page.getByRole("button", { name: "Open workspace" }).click();
  await expect(
    moy.getByText("Configured account: OWNERSHIP_CONFLICT"),
  ).toBeVisible();
  await expect(moy.getByText(/reassignment is unavailable/)).toBeVisible();
  await moy.getByRole("link", { name: "Products" }).click();
  await expect(page.getByRole("heading", { name: /Products/ })).toBeVisible();
  await page
    .getByRole("navigation")
    .getByRole("link", { name: "Warehouses" })
    .click();
  await expect(page.getByRole("heading", { name: /Warehouses/ })).toBeVisible();
  const observedResponses = (await Promise.all(jsonResponses)).join("\n");
  expect(observedResponses).not.toContain("synthetic-only-key");
  expect(observedResponses).not.toContain("synthetic-only-token");
});
