import { expect, test, type Page } from "@playwright/test";
import { mkdirSync } from "node:fs";
import { join } from "node:path";

const origin = process.env.STYK_CATALOG_STACK_ORIGIN || "";
const testAPI = process.env.STYK_CATALOG_STACK_API || "";
const testSecret = process.env.STYK_CATALOG_STACK_SECRET || "";
const scope = {
  tenant: "cross-e2e",
  moysklad_account: "2928cf35-d30a-11f1-0a80-1b9000000355",
  ozon_account: "1549477",
};
const sourceName = "Название из МойСклад";
const ozonDescription = "Описание из Ozon для смешанного направления";
const unmappedName = "Новый товар Ozon без соответствия";
const unmappedDescription = "Описание нового товара из Ozon";
const createdID = "11111111-1111-4111-8111-111111111111";
const password = "synthetic-catalog-browser-password";
const aEmail = "catalog-stack-a@example.test";
const bEmail = "catalog-stack-b@example.test";
const catalogURL = (resource: string) =>
  `${origin}/api/catalog-sync/${resource}?${new URLSearchParams(scope)}`;

type Preview = {
  id: string;
  decisions: {
    enrollment_id: string;
    ready: boolean;
    validation: { code: string }[];
    target_absent?: boolean;
    plan: {
      changes: { field: string; target: { account: { provider: string } } }[];
      noop: boolean;
    };
    commands: { values: Record<string, unknown> }[];
  }[];
};
type Operation = {
  id: string;
  state: string;
  targets: {
    state: string;
    receipt: { task_id?: string };
    created_target?: { id: string };
  }[];
};

test("merchant catalog review dispatches mixed fields through the real stack and survives reload", async ({
  browser,
}) => {
  test.skip(
    !origin || !testAPI || !testSecret,
    "requires isolated PostgreSQL and loopback provider adapters",
  );
  test.setTimeout(150_000);
  expect(new URL(origin).hostname).toBe("127.0.0.1");
  expect(new URL(testAPI).hostname).toBe("127.0.0.1");
  const aContext = await browser.newContext({
    viewport: { width: 1440, height: 1000 },
  });
  const bContext = await browser.newContext();
  const a = await aContext.newPage();
  const b = await bContext.newPage();
  try {
    for (const page of [a, b]) {
      page.setDefaultTimeout(12_000);
      await page.route("**/*", (route) =>
        ["127.0.0.1", "localhost"].includes(
          new URL(route.request().url()).hostname,
        )
          ? route.continue()
          : route.abort(),
      );
    }
    await register(a, aEmail);
    await register(b, bEmail);
    const claim = await a.request.post(`${testAPI}/__test/claim`, {
      headers: testHeaders(),
      data: { email: aEmail },
    });
    expect(claim.status()).toBe(204);
    await a.reload();
    await login(a, aEmail);
    await selectAccounts(a);
    await a.getByRole("link", { name: "Каталог", exact: true }).click();
    await expect(
      a.getByRole("heading", { name: "Синхронизация каталога", exact: true }),
    ).toBeVisible();
    const emptyConfig = await (
      await a.request.get(catalogURL("config"))
    ).json();
    expect(emptyConfig).toMatchObject({
      enabled: false,
      revision: 0,
      targets: [],
      scope,
    });
    await configureMixedFields(a);
    await a.getByLabel(`Выбрать товар ${sourceName}`, { exact: true }).check();
    const preview = await review(a);
    expect(preview.decisions).toHaveLength(1);
    expect(preview.decisions[0]).toMatchObject({ ready: true, validation: [] });
    expect(
      preview.decisions[0].plan.changes
        .map((change) => [change.field, change.target.account.provider])
        .sort(),
    ).toEqual([
      ["description", "moysklad"],
      ["name", "ozon"],
    ]);
    expect(
      preview.decisions[0].commands.map((command) =>
        Object.keys(command.values).sort(),
      ),
    ).toEqual([["description"], ["name"]]);
    expect(await providerState(a)).toMatchObject({
      moy_writes: 0,
      ozon_writes: 0,
      polls: 0,
    });
    await assertOtherMerchantDenied(b, preview);
    await expect(a.getByText(ozonDescription, { exact: true })).toBeVisible();
    await capture(a, "catalog-preview-desktop.png");
    await a
      .getByLabel(`Подтвердить изменения товара ${sourceName}`, { exact: true })
      .check();
    await a.getByLabel(/Я проверил изменения выбранных товаров/).check();
    const approvalResponse = a.waitForResponse(
      (response) =>
        response.url().includes("/api/catalog-sync/approve") &&
        response.request().method() === "POST",
    );
    await a
      .getByRole("button", { name: "Отправить изменения (1)", exact: true })
      .click();
    const approval = await approvalResponse;
    expect(approval.status(), await approval.text()).toBe(200);
    const operation = (await approval.json()) as Operation;
    await expect
      .poll(
        async () =>
          (await operations(a))
            .find((item) => item.id === operation.id)
            ?.targets.some(
              (work) =>
                work.state === "PROCESSING" && work.receipt.task_id === "91",
            ),
        { timeout: 25_000, intervals: [100, 250, 500] },
      )
      .toBe(true);
    const processing = await providerState(a);
    expect(processing.moy_writes).toBeLessThanOrEqual(1);
    expect(processing).toMatchObject({
      ozon_writes: 1,
      polls: 0,
      ozon_name: "Старое название Ozon",
    });
    await a
      .getByRole("button", { name: "Обновить статусы", exact: true })
      .click();
    await expect(
      a
        .locator(".catalog-work")
        .filter({ hasText: "Ozon:" })
        .getByText("Сервис обрабатывает запрос", { exact: true }),
    ).toBeVisible();
    await expect
      .poll(
        async () =>
          (await operations(a))
            .find((item) => item.id === operation.id)
            ?.targets.map((work) => work.state),
        { timeout: 45_000, intervals: [250, 500, 1000] },
      )
      .toEqual(["RECONCILED", "RECONCILED"]);
    await a
      .getByRole("button", { name: "Обновить статусы", exact: true })
      .click();
    await expect(
      a.getByText("Изменения подтверждены сверкой", { exact: true }),
    ).toHaveCount(3);
    await a.reload();
    await expect(
      a.getByText("Изменения подтверждены сверкой", { exact: true }),
    ).toHaveCount(3);
    expect(await providerState(a)).toMatchObject({
      moy_writes: 1,
      ozon_writes: 1,
      ozon_name: sourceName,
      moy_description: ozonDescription,
    });
    await a.getByLabel(`Выбрать товар ${sourceName}`, { exact: true }).check();
    const noop = await review(a);
    expect(noop.decisions[0].plan.noop).toBe(true);
    expect(noop.decisions[0].commands).toHaveLength(0);
    await expect(
      a.getByText("Изменений для отправки нет.", { exact: true }),
    ).toBeVisible();
    await expect(
      a.getByRole("button", { name: "Отправить изменения (0)", exact: true }),
    ).toBeDisabled();
    expect(await operations(a)).toHaveLength(1);
    expect(await providerState(a)).toMatchObject({
      moy_writes: 1,
      ozon_writes: 1,
    });
    await screenshots(a);
    await creationFlow(a);
  } finally {
    await aContext.close();
    await bContext.close();
  }
});

async function creationFlow(page: Page) {
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page
    .getByLabel(`Выбрать товар ${sourceName}`, { exact: true })
    .uncheck();
  await page
    .getByRole("combobox", { name: /Список товаров/ })
    .selectOption("ozon");
  const row = page.locator("tbody tr").filter({ hasText: "CATALOG-UNMAPPED" });
  await row
    .getByRole("button", { name: "Создать в МойСклад", exact: true })
    .click();
  await expect(
    page.getByLabel("Название нового товара", { exact: true }),
  ).toHaveValue(unmappedName);
  await page
    .getByLabel("Описание нового товара", { exact: true })
    .fill(unmappedDescription);
  const preview = await review(page);
  expect(preview.decisions[0]).toMatchObject({
    ready: true,
    target_absent: true,
    validation: [],
  });
  expect(preview.decisions[0].commands).toHaveLength(1);
  expect(preview.decisions[0].commands[0]).toMatchObject({
    action: "create",
    target: { account: { provider: "moysklad" }, id: "" },
    values: {
      name: { state: "known", data: unmappedName },
      description: { state: "known", data: unmappedDescription },
    },
  });
  expect(await providerState(page)).toMatchObject({ moy_creates: 0 });
  await expect(
    page.getByText("Будет создан новый товар", { exact: true }),
  ).toBeVisible();
  await capture(page, "catalog-create-preview.png");
  await page
    .getByLabel(`Подтвердить изменения товара ${unmappedName}`, { exact: true })
    .check();
  await page.getByLabel(/Я проверил изменения выбранных товаров/).check();
  const approved = page.waitForResponse(
    (response) =>
      response.url().includes("/api/catalog-sync/approve") &&
      response.request().method() === "POST",
  );
  await page
    .getByRole("button", { name: "Отправить изменения (1)", exact: true })
    .click();
  const response = await approved;
  expect(response.status(), await response.text()).toBe(200);
  const operation = (await response.json()) as Operation;
  await expect
    .poll(
      async () =>
        (await operations(page)).find((item) => item.id === operation.id)
          ?.targets[0],
      { timeout: 25_000, intervals: [250, 500, 1000] },
    )
    .toMatchObject({ state: "RECONCILED", created_target: { id: createdID } });
  const session = await (
    await page.request.get(`${origin}/api/session`)
  ).json();
  const replay = await page.request.post(`${origin}/api/catalog-sync/approve`, {
    headers: { Origin: origin, "X-STYK-CSRF": session.csrf },
    data: response.request().postDataJSON(),
  });
  expect(replay.status(), await replay.text()).toBe(200);
  expect((await replay.json()).id).toBe(operation.id);
  expect(await operations(page)).toHaveLength(2);
  expect(await providerState(page)).toMatchObject({
    moy_creates: 1,
    moy_writes: 1,
    ozon_writes: 1,
  });
  await page.reload();
  await page
    .getByRole("combobox", { name: /Список товаров/ })
    .selectOption("ozon");
  await page
    .getByLabel(`Выбрать товар ${unmappedName}`, { exact: true })
    .check();
  const repeated = await review(page);
  expect(repeated.decisions[0].ready).toBe(false);
  expect(repeated.decisions[0].commands || []).toHaveLength(0);
  expect(repeated.decisions[0].validation.map((issue) => issue.code)).toContain(
    "CREATED_TARGET_REQUIRES_MAPPING",
  );
  await expect(
    page.getByText(
      "Товар уже создан. Подтвердите соответствие товаров перед дальнейшей синхронизацией.",
      { exact: false },
    ),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Отправить изменения (0)", exact: true }),
  ).toBeDisabled();
  expect(await providerState(page)).toMatchObject({ moy_creates: 1 });
  await page.setViewportSize({ width: 390, height: 844 });
  await capture(page, "catalog-created-pending-mapping-mobile.png");
}

async function configureMixedFields(page: Page) {
  await page
    .getByLabel("Разрешить синхронизацию каталога для выбранных аккаунтов", {
      exact: true,
    })
    .check();
  await page
    .locator("#catalog-policy-name")
    .selectOption("MOYSKLAD_AUTHORITATIVE");
  await page
    .locator("#catalog-policy-description")
    .selectOption("OZON_AUTHORITATIVE");
  const saved = page.waitForResponse(
    (response) =>
      response.url().includes("/api/catalog-sync/config") &&
      response.request().method() === "POST",
  );
  await page
    .getByRole("button", { name: "Сохранить настройки", exact: true })
    .click();
  const response = await saved;
  expect(response.status(), await response.text()).toBe(200);
  expect(await response.json()).toMatchObject({
    enabled: true,
    test_enabled: true,
    rules: {
      name: "MOYSKLAD_AUTHORITATIVE",
      description: "OZON_AUTHORITATIVE",
    },
  });
  await expect(
    page.getByRole("button", { name: "Сохранить настройки", exact: true }),
  ).toBeDisabled();
}

async function review(page: Page): Promise<Preview> {
  const reviewed = page.waitForResponse(
    (response) =>
      response.url().includes("/api/catalog-sync/preview") &&
      response.request().method() === "POST",
  );
  await page
    .getByRole("button", { name: "Проверить выбранные товары", exact: true })
    .click();
  const response = await reviewed;
  expect(response.status(), await response.text()).toBe(200);
  const value = (await response.json()) as Preview;
  await expect(
    page.getByRole("heading", {
      name: "Проверьте изменения перед отправкой",
      exact: true,
    }),
  ).toBeVisible();
  return value;
}

async function assertOtherMerchantDenied(page: Page, preview: Preview) {
  const session = await (
    await page.request.get(`${origin}/api/session`)
  ).json();
  const headers = { Origin: origin, "X-STYK-CSRF": session.csrf };
  for (const resource of ["config", "products", "operations"]) {
    const response = await page.request.get(catalogURL(resource));
    expect(response.status(), `merchant B read A ${resource}`).toBe(404);
    const accounts = new URLSearchParams({
      moysklad_account: scope.moysklad_account,
      ozon_account: scope.ozon_account,
    });
    const implicitTenant = await page.request.get(
      `${origin}/api/catalog-sync/${resource}?${accounts}`,
    );
    expect(
      implicitTenant.status(),
      `merchant B reused A provider accounts for ${resource}`,
    ).toBe(404);
  }
  for (const [resource, data] of [
    [
      "config",
      {
        config: {
          scope,
          revision: 1,
          enabled: true,
          test_enabled: true,
          interval_seconds: 60,
          rules: {},
          targets: [],
        },
        expected_revision: 1,
        idempotency_key: "forged-config",
      },
    ],
    [
      "preview",
      {
        expected_revision: 2,
        enrollment_ids: [preview.decisions[0].enrollment_id],
      },
    ],
    [
      "approve",
      {
        preview_id: preview.id,
        enrollment_ids: [preview.decisions[0].enrollment_id],
        acknowledge: true,
        idempotency_key: "forged-approval",
      },
    ],
  ] as const) {
    const response = await page.request.post(
      `${origin}/api/catalog-sync/${resource}`,
      { headers, data: { ...scope, ...data } },
    );
    expect(response.status(), `merchant B wrote A ${resource}`).toBe(404);
  }
}

async function operations(page: Page): Promise<Operation[]> {
  const response = await page.request.get(catalogURL("operations"));
  expect(response.status()).toBe(200);
  return (await response.json()).operations;
}

async function providerState(page: Page) {
  const response = await page.request.get(`${testAPI}/__test/catalog-state`, {
    headers: testHeaders(),
  });
  expect(response.status()).toBe(200);
  return response.json();
}

function testHeaders() {
  return { "X-Test-Secret": testSecret };
}

async function register(page: Page, email: string) {
  await page.goto(origin);
  await page
    .getByRole("button", { name: "Создать аккаунт", exact: true })
    .click();
  await page.getByLabel("Электронная почта", { exact: true }).fill(email);
  await page
    .getByRole("button", { name: "Создать аккаунт", exact: true })
    .click();
  await expect(page.getByText(/Если адрес доступен/)).toBeVisible();
  const mail = await page.request.get(`${testAPI}/__test/mail`, {
    params: { email },
    headers: testHeaders(),
  });
  const { token } = await mail.json();
  expect(token).toBeTruthy();
  await page.goto(`${origin}/#verify=${token}`);
  await page.getByLabel("Новый пароль", { exact: true }).fill(password);
  await page
    .getByRole("button", { name: "Подтвердить адрес", exact: true })
    .click();
  await expect(page.getByText(/Почта подтверждена/)).toBeVisible();
  await login(page, email);
}

async function login(page: Page, email: string) {
  await page.getByLabel("Электронная почта", { exact: true }).fill(email);
  await page.getByLabel("Пароль", { exact: true }).fill(password);
  const signedIn = page.waitForResponse(
    (response) =>
      response.url().endsWith("/api/session") &&
      response.request().method() === "POST",
    { timeout: 20_000 },
  );
  await page.getByRole("button", { name: "Войти", exact: true }).click();
  const response = await signedIn;
  expect(response.status()).toBe(204);
  await expect(
    page.getByRole("link", { name: "Каталог", exact: true }),
  ).toBeVisible({ timeout: 20_000 });
}

async function selectAccounts(page: Page) {
  const selector = page.locator(".workspace-selector");
  if ((await selector.getAttribute("open")) === null)
    await selector.locator("summary").click();
  await selector
    .getByRole("combobox", { name: "МойСклад", exact: true })
    .selectOption(scope.moysklad_account);
  await selector
    .getByRole("combobox", { name: "Ozon", exact: true })
    .selectOption(scope.ozon_account);
  await selector.locator("summary").click();
}

async function screenshots(page: Page) {
  await capture(page, "catalog-desktop.png");
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(
    page.getByRole("heading", { name: "Синхронизация каталога", exact: true }),
  ).toBeVisible();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth + 1,
    ),
  ).toBe(true);
  await capture(page, "catalog-mobile.png");
}

async function capture(page: Page, name: string) {
  const directory =
    process.env.STYK_CATALOG_STACK_SCREENSHOTS_DIR || "/tmp/styk-catalog-stack";
  mkdirSync(directory, { recursive: true });
  await page.screenshot({ path: join(directory, name), fullPage: true });
}
