import { expect, test } from "@playwright/test";

test("operator queues, reconciles and observes a worker import without marketplace writes", async ({
  page,
  request,
}) => {
  test.setTimeout(90_000);
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
  await page.getByRole("combobox", { name: "Ozon" }).selectOption(account);
  await page
    .getByRole("navigation")
    .getByRole("link", { name: "Integrations" })
    .click();
  const panel = page.getByRole("region", { name: "Ozon catalog imports" });
  await expect(
    panel.getByText("No import operations for this account."),
  ).toBeVisible();
  expect((await state()).operations).toBe(0);

  const submit = async () => {
    await panel.getByRole("button", { name: "Import catalog" }).click();
    const dialog = page.getByRole("dialog", {
      name: "Confirm Ozon catalog import",
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
  await page.getByRole("button", { name: "Retry same import request" }).click();
  await expect(
    page.getByText("The existing import operation was recovered."),
  ).toBeVisible();
  expect(bodies).toHaveLength(2);
  expect(bodies[1]).toBe(bodies[0]);
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
      /Products observed 1; warehouses observed 1; stock observations 1/,
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
  await page.getByLabel("Tenant", { exact: true }).fill(tenant);
  await page.getByRole("button", { name: "Open workspace" }).click();
  await page.getByRole("combobox", { name: "Ozon" }).selectOption(account);
  await expect(
    panel.getByText("Catalog traversal completed and a batch is saved."),
  ).toBeVisible();
  await expect(panel.getByText(/Completed batch:/)).toBeVisible();
  const observed = (await Promise.all(responses)).join("\n");
  expect(observed).not.toContain("synthetic-import-key");
  expect((await state()).operations).toBe(2);
});
