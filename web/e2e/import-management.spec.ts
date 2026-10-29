import { expect, test } from "@playwright/test";
import { signIn, selectWorkspace, navigate } from "./workspace";

test("operator queues, reconciles and observes a worker import without marketplace writes", async ({
  page,
  request,
}) => {
  test.setTimeout(90_000);
  const provider = process.env.STYK_IMPORT_E2E_PROVIDER || "ozon";
  const name = provider === "moysklad" ? "МойСклад" : "Ozon";
  const other = provider === "moysklad" ? "Ozon" : "МойСклад";
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
  await signIn(page, "ssssssssssssssssssssssssssssssss", origin);
  await selectWorkspace(
    page,
    tenant,
    provider === "moysklad" ? account : "",
    provider === "ozon" ? account : "",
  );
  await navigate(page, "Интеграции");
  const panel = page.getByRole("region", { name: `${name}: импорт данных` });
  await expect(
    panel.getByText(
      "Каталог ещё не загружен. Загрузите товары, склады и остатки.",
    ),
  ).toBeVisible();
  expect((await state()).operations).toBe(0);

  const submit = async () => {
    await panel.getByRole("button", { name: "Загрузить данные" }).click();
    const dialog = page.getByRole("dialog", {
      name: `Загрузить данные ${name}?`,
    });
    await expect(
      dialog.getByRole("button", { name: "Подтвердить загрузку" }),
    ).toBeDisabled();
    await dialog.getByRole("checkbox").check();
    await dialog.getByRole("button", { name: "Подтвердить загрузку" }).click();
  };
  await submit();
  await expect(
    panel.getByText("Загрузка начнётся, когда освободится обработчик."),
  ).toBeVisible();
  expect(await state()).toMatchObject({ operations: 1, tasks: 1, batches: 0 });
  await panel.getByRole("button", { name: "Отменить загрузку" }).click();
  await page
    .getByRole("dialog", { name: "Отменить загрузку?" })
    .getByRole("button", { name: "Подтвердить отмену" })
    .click();
  await expect(
    panel.getByText("Загрузка отменена до начала обработки."),
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
  await expect(page.getByText(/Результат отправки неизвестен/)).toBeVisible();
  expect(await state()).toMatchObject({ operations: 2, tasks: 2, batches: 0 });
  const selectAccount = async (providerName: string, value: string) => {
    const card = page.locator(".integration-card").filter({
      has: page.getByRole("heading", { name: providerName, exact: true }),
    });
    await card
      .getByRole("button", { name: /^(Настройки|Выбрать аккаунт)$/ })
      .click();
    const dialog = page.getByRole("dialog", {
      name: `Подключение ${providerName}`,
    });
    await dialog
      .getByRole("combobox", { name: `${providerName}: аккаунт` })
      .selectOption(value);
    await dialog.getByRole("button", { name: "Закрыть настройки" }).click();
  };
  await selectAccount(name, "");
  await selectAccount(other, account);
  const otherPanel = page.getByRole("region", {
    name: `${other}: импорт данных`,
  });
  await expect(
    otherPanel.getByText(
      "Каталог ещё не загружен. Загрузите товары, склады и остатки.",
    ),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Повторить тот же запрос" }),
  ).toHaveCount(0);
  await page
    .getByRole("button", { name: "Вернуться к исходному аккаунту" })
    .click();
  await page.getByRole("button", { name: "Повторить тот же запрос" }).click();
  await expect(
    page.getByText(
      "Ранее созданная загрузка найдена. Повторный запрос не создал новую.",
    ),
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
  await expect(panel.getByText(`Обновляем данные ${name}`)).toBeVisible({
    timeout: 10_000,
  });
  await expect(
    panel.getByText("Загружаем товары…", { exact: true }),
  ).toBeVisible();
  await request.post(`${api}/__test/release`);
  await expect(
    panel.getByRole("button", { name: "Обновить данные", exact: true }),
  ).toBeEnabled({ timeout: 15_000 });
  const history = page.locator(
    `details.provider-history[aria-label="${name}: история обновлений"]`,
  );
  const inspectSuccess = async () => {
    await page
      .getByRole("button", { name: "История обновлений", exact: true })
      .click();
    if (!(await history.evaluate((node) => (node as HTMLDetailsElement).open)))
      await history.locator(":scope > summary").click();
    const entry = history
      .locator("details.history-entry")
      .filter({ hasText: "Успешно" })
      .first();
    await expect(entry).toBeVisible();
    if (!(await entry.evaluate((node) => (node as HTMLDetailsElement).open)))
      await entry.locator(":scope > summary").click();
    await expect(
      entry.getByText("Товары и остатки загружены", { exact: true }).last(),
    ).toBeVisible();
    await entry.locator("details.technical-details > summary").click();
    await expect(
      entry.getByText("Пакет данных", { exact: true }),
    ).toBeVisible();
    await expect(
      entry.getByText(
        provider === "moysklad"
          ? /Обнаружено: товаров 1, складов 1, записей об остатках 1, вариантов 1, записей по складам 1/
          : /Обнаружено: товаров 1, складов 1, записей об остатках 1/,
      ),
    ).toBeVisible();
  };
  await inspectSuccess();
  await page
    .getByRole("dialog", { name: "История обновлений", exact: true })
    .getByRole("button", { name: "Закрыть", exact: true })
    .click();
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
  await page.locator(".workspace-selector > summary").click();
  await expect(
    page.getByLabel("Рабочее пространство", { exact: true }),
  ).toHaveValue(tenant);
  await expect(page.getByRole("combobox", { name, exact: true })).toHaveValue(
    account,
  );
  await page.locator(".workspace-selector > summary").click();
  await expect(panel.getByText(/^Данные обновлены/)).toBeVisible();
  await inspectSuccess();
  await page
    .getByRole("dialog", { name: "История обновлений", exact: true })
    .getByRole("button", { name: "Закрыть", exact: true })
    .click();
  await expect(
    otherPanel.getByText(
      "Каталог ещё не загружен. Загрузите товары, склады и остатки.",
    ),
  ).toBeVisible();
  // The same still-running worker must also execute the other provider.
  await otherPanel.getByRole("button", { name: "Загрузить данные" }).click();
  const otherDialog = page.getByRole("dialog", {
    name: `Загрузить данные ${other}?`,
  });
  await otherDialog.getByRole("checkbox").check();
  await otherDialog
    .getByRole("button", { name: "Подтвердить загрузку" })
    .click();
  await expect(
    otherPanel.getByRole("button", { name: "Обновить данные", exact: true }),
  ).toBeEnabled({ timeout: 15000 });
  const otherHistoryPanel = page.locator(
    `details.provider-history[aria-label="${other}: история обновлений"]`,
  );
  await page
    .getByRole("button", { name: "История обновлений", exact: true })
    .click();
  await otherHistoryPanel.locator(":scope > summary").click();
  await expect(
    otherHistoryPanel.getByText("Успешно", { exact: true }).last(),
  ).toBeVisible();
  await otherHistoryPanel.locator(":scope > summary").click();
  await history.locator(":scope > summary").click();
  expect(JSON.parse(bodies[2])[otherField]).toBe(account);
  expect(JSON.parse(bodies[2])[field]).toBeUndefined();
  await page
    .getByRole("dialog", { name: "История обновлений", exact: true })
    .getByRole("button", { name: "Закрыть", exact: true })
    .click();
  const html = await page.content();
  expect(html).not.toContain("synthetic-import-key");
  expect(html).not.toContain("synthetic-moy-import-token");
  expect(await page.locator("body").innerText()).not.toContain(account);
  expect(await page.locator("body").innerText()).not.toContain(tenant);
  await page.setViewportSize({ width: 1440, height: 900 });
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
  await page
    .getByRole("link", { name: "Сопоставить склады", exact: true })
    .click();
  await expect(
    page.getByRole("heading", { name: "Сопоставление складов" }).first(),
  ).toBeVisible();
  await expect(
    page.getByText("Synthetic warehouse", { exact: true }),
  ).toBeVisible();
  await page
    .getByRole("navigation")
    .getByRole("link", { name: "Товары" })
    .click();
  await expect(
    page
      .locator('td[data-label="Товар в Ozon"]')
      .filter({ hasText: "synthetic-offer" }),
  ).toBeVisible();
  await page.locator(".workspace-selector > summary").click();
  await page.getByRole("link", { name: "Диагностика", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Диагностика", exact: true }),
  ).toBeVisible();
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
