import { expect, test } from "@playwright/test";

test("operator queues, reconciles and observes a worker import without marketplace writes", async ({
  page,
  request,
}) => {
  test.setTimeout(90_000);
  const provider = process.env.STYK_IMPORT_E2E_PROVIDER || "ozon";
  const name = provider === "moysklad" ? "MoySklad" : "Ozon";
  const other = provider === "moysklad" ? "Ozon" : "MoySklad";
  const field = provider === "moysklad" ? "moysklad_account" : "ozon_account";
  const otherField =
    provider === "moysklad" ? "ozon_account" : "moysklad_account";
  const api = process.env.STYK_IMPORT_E2E_URL!;
  const tenant = process.env.STYK_IMPORT_E2E_TENANT!;
  const account = process.env.STYK_IMPORT_E2E_ACCOUNT!;
  const origin = "http://127.0.0.1:15175";
  const responses: Promise<string>[] = [];
  page.on("response", (response) => {
    if (
      response.url().includes("/api/") &&
      (response.headers()["content-type"] || "").includes("application/json")
    )
      responses.push(response.text().catch(() => ""));
  });
  const state = async () => (await request.get(`${api}/__test/state`)).json();
  expect((await state()).operations).toBe(0);
  await page.goto(origin);
  await page
    .getByLabel("Operator secret")
    .fill("ssssssssssssssssssssssssssssssss");
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.getByLabel("Tenant", { exact: true }).fill(tenant);
  await page.getByRole("button", { name: "Open workspace" }).click();
  await page.getByRole("combobox", { name }).selectOption(account);
  await page
    .getByRole("navigation")
    .getByRole("link", { name: "Integrations" })
    .click();
  const panel = page.getByRole("region", { name: `${name} catalog imports` });
  await expect(
    panel.getByText("No import operations for this account."),
  ).toBeVisible();
  expect((await state()).operations).toBe(0);

  const submit = async () => {
    await panel.getByRole("button", { name: "Import catalog" }).click();
    const dialog = page.getByRole("dialog", {
      name: `Confirm ${name} catalog import`,
    });
    await expect(
      dialog.getByRole("button", { name: "Confirm import" }),
    ).toBeDisabled();
    await dialog.getByRole("checkbox").check();
    await dialog.getByRole("button", { name: "Confirm import" }).click();
  };
  await submit();
  await expect(
    panel.getByText("Waiting for an available worker."),
  ).toBeVisible();
  expect(await state()).toMatchObject({ operations: 1, tasks: 1, batches: 0 });
  await panel.getByRole("button", { name: "Cancel queued import" }).click();
  await page
    .getByRole("dialog", { name: "Cancel queued import" })
    .getByRole("button", { name: "Confirm cancellation" })
    .click();
  await expect(
    panel.getByText("Import was cancelled before execution."),
  ).toBeVisible();
  expect((await state()).batches).toBe(0);

  const bodies: string[] = [];
  await page.route("**/api/imports", async (route) => {
    if (route.request().method() !== "POST") return route.continue();
    bodies.push(route.request().postData() || "");
    if (bodies.length === 1) {
      await route.fetch();
      await route.abort("failed");
    } else {
      await route.continue();
    }
  });
  await submit();
  await expect(page.getByText(/Submission outcome unknown/)).toBeVisible();
  expect(await state()).toMatchObject({ operations: 2, tasks: 2, batches: 0 });
  await page.getByRole("combobox", { name }).selectOption("");
  await page.getByRole("combobox", { name: other }).selectOption(account);
  const otherPanel = page.getByRole("region", {
    name: `${other} catalog imports`,
  });
  await expect(
    otherPanel.getByText("No import operations for this account."),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Retry same import request" }),
  ).toHaveCount(0);
  await page
    .getByRole("button", { name: "Return to original import scope" })
    .click();
  await page.getByRole("button", { name: "Retry same import request" }).click();
  await expect(
    page.getByText("The existing import operation was recovered."),
  ).toBeVisible();
  expect(bodies).toHaveLength(2);
  expect(bodies[1]).toBe(bodies[0]);
  expect(JSON.parse(bodies[0])[field]).toBe(account);
  expect(JSON.parse(bodies[0])[otherField]).toBeUndefined();
  expect(JSON.parse(bodies[0]).limits).toBeUndefined();
  const stored = await page.evaluate(() =>
    JSON.stringify({
      local: { ...localStorage },
      session: { ...sessionStorage },
    }),
  );
  expect(stored).not.toContain(JSON.parse(bodies[0]).idempotency_key);
  expect(stored).not.toContain("synthetic-import-key");
  expect(stored).not.toContain("synthetic-moy-import-token");
  expect(JSON.parse(bodies[0]).idempotency_key).toMatch(/^[0-9a-f-]{36}$/);
  expect(await state()).toMatchObject({ operations: 2, tasks: 2, batches: 0 });
  await request.post(`${api}/__test/start-worker`);
  await expect.poll(async () => (await state()).started).toBe(true);
  await expect(panel.getByText("Import in progress.")).toBeVisible({
    timeout: 10_000,
  });
  await expect(panel.getByText(/READING_PRODUCTS/).first()).toBeVisible();
  await request.post(`${api}/__test/release`);
  await expect(
    panel.getByText("Catalog traversal completed and a batch is saved."),
  ).toBeVisible({ timeout: 15_000 });
  await expect(panel.getByText(/Completed batch:/)).toBeVisible();
  await expect(
    panel.getByText(
      provider === "moysklad"
        ? /Products observed 1; variants observed 1; warehouses observed 1; aggregate stock observations 1; warehouse stock observations 1/
        : /Products observed 1; warehouses observed 1; stock observations 1/,
    ),
  ).toBeVisible();
  expect(await state()).toMatchObject({
    operations: 2,
    tasks: 2,
    batches: 1,
    products: 1,
    warehouses: 1,
    stock: 1,
    linked: 1,
    publication: 0,
  });
  await page.reload();
  await expect(page.getByLabel("Tenant", { exact: true })).toHaveValue(tenant);
  await expect(page.getByRole("combobox", { name })).toHaveValue(account);
  await expect(
    panel.getByText("Catalog traversal completed and a batch is saved."),
  ).toBeVisible();
  await expect(panel.getByText(/Completed batch:/)).toBeVisible();
  await expect(
    otherPanel.getByText("No import operations for this account."),
  ).toBeVisible();
  // The same still-running worker must also execute the other provider.
  await otherPanel.getByRole("button", { name: "Import catalog" }).click();
  const otherDialog = page.getByRole("dialog", {
    name: `Confirm ${other} catalog import`,
  });
  await otherDialog.getByRole("checkbox").check();
  await otherDialog.getByRole("button", { name: "Confirm import" }).click();
  await expect(
    otherPanel.getByText("Catalog traversal completed and a batch is saved."),
  ).toBeVisible({ timeout: 15000 });
  expect(JSON.parse(bodies[2])[otherField]).toBe(account);
  expect(JSON.parse(bodies[2])[field]).toBeUndefined();
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.screenshot({
    path: `test-results/imports-${provider}.png`,
    fullPage: true,
  });
  await panel.screenshot({ path: `test-results/import-panel-${provider}.png` });
  await page.setViewportSize({ width: 390, height: 844 });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
  await panel.getByRole("link", { name: "View latest warehouses" }).click();
  await expect(
    page.getByRole("heading", { name: /Warehouses/ }).first(),
  ).toBeVisible();
  await expect(
    page.getByText("Synthetic warehouse", { exact: true }),
  ).toBeVisible();
  await page
    .getByRole("navigation")
    .getByRole("link", { name: "Products" })
    .click();
  await expect(
    page.getByRole("cell", { name: /^synthetic-offer Review$/ }),
  ).toBeVisible();
  await page
    .getByRole("navigation")
    .getByRole("link", { name: "Diagnostics" })
    .click();
  await expect(page.getByText("Publication disabled")).toBeVisible();
  const observed = (await Promise.all(responses)).join("\n");
  expect(observed).not.toContain("synthetic-import-key");
  expect(observed).not.toContain("synthetic-moy-import-token");
  expect((await state()).operations).toBe(2);
  const otherHistory = await page.request.get(
    `${api}/api/imports?tenant=${encodeURIComponent(tenant)}&${otherField}=${encodeURIComponent(account)}`,
  );
  expect(otherHistory.status()).toBe(200);
  const otherItems = (await otherHistory.json()).items;
  expect(otherItems).toHaveLength(1);
  expect(otherItems[0].provider).toBe(
    provider === "moysklad" ? "ozon" : "moysklad",
  );
  expect(otherItems[0].state).toBe("SUCCEEDED");
});
