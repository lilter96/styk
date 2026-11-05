import { expect, test } from "@playwright/test";

const origin = process.env.STYK_ORDERS_STACK_ORIGIN || "";
const serializedScope = process.env.STYK_ORDERS_STACK_SCOPE || "";
const control = process.env.STYK_ORDERS_MAIL_SECRET || "";

test("current posting state stays independent of paginated history and reconciles by reading", async ({
  page,
}) => {
  test.skip(
    !origin || !serializedScope || !control,
    "requires isolated merchant current-state stack",
  );
  test.setTimeout(120_000);
  expect(new URL(origin).hostname).toBe("127.0.0.1");
  const scope = JSON.parse(serializedScope) as {
    tenant: string;
    moysklad_account: string;
    ozon_account: string;
  };
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  await page.route("**/*", (route) =>
    new URL(route.request().url()).hostname === "127.0.0.1"
      ? route.continue()
      : route.abort(),
  );
  await page.goto(origin);
  await page
    .getByLabel("Электронная почта", { exact: true })
    .fill("native-process-a@example.test");
  await page
    .getByLabel("Пароль", { exact: true })
    .fill("synthetic-orders-process-password");
  await page.getByRole("button", { name: "Войти", exact: true }).click();
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
  await page.getByRole("link", { name: "Заказы", exact: true }).click();
  await page
    .getByLabel("Найти отправление или заказ")
    .fill("position-http-posting");
  const posting = page
    .locator(".orders-postings > .orders-posting")
    .filter({ hasText: "position-http-posting" });
  const checkbox = page.getByLabel(
    "Выбрать отправление position-http-posting",
    { exact: true },
  );
  await expect(posting).toContainText(
    "Последняя операция: Результат неизвестен",
  );
  await expect(checkbox).toBeDisabled();
  const history = page.locator(
    'section[aria-labelledby="orders-history-heading"]',
  );
  await expect(
    history
      .locator(".orders-work")
      .filter({ hasText: "position-http-posting" }),
  ).toHaveCount(0);
  await history
    .getByRole("button", { name: "Следующие операции", exact: true })
    .click();
  await expect(history.getByText("Страница 2", { exact: true })).toBeVisible();
  const unknown = history
    .locator(".orders-work")
    .filter({ hasText: "Создать отгрузку в МойСклад" });
  await expect(unknown).toContainText("Результат неизвестен");
  await expect(unknown).not.toContainText(
    "Складской документ подтверждён сверкой",
  );
  await expect(
    unknown.getByRole("link", { name: /^Заказ МойСклад / }),
  ).toBeVisible();
  await expect(checkbox).toBeDisabled();
  const details = unknown.getByText(
    "Проверить неизвестный результат и варианты восстановления",
    { exact: true },
  );
  await details.focus();
  await page.keyboard.press("Enter");
  await expect(unknown).toContainText(
    "Для этого действия доступна только проверка",
  );
  await expect(
    unknown.getByRole("button", { name: "Записать разрешение нового решения" }),
  ).toHaveCount(0);
  await page.screenshot({
    path: "test-results/orders-current-state-desktop.png",
    fullPage: true,
  });
  const response = await page.request.post(
    `${origin}/__test/orders/merchant-reconcile`,
    { headers: { "X-Orders-Test-Control": control } },
  );
  expect(response.status()).toBe(204);
  await expect(posting).toContainText(
    "Последняя операция: Подтверждено сверкой",
    { timeout: 25_000 },
  );
  await expect(checkbox).toBeEnabled();
  await expect(history.getByText("Страница 2", { exact: true })).toBeVisible();
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(posting.getByText(/Состояние STYK проверено:/)).toBeVisible();
  const widths = await page.evaluate(() => ({
    viewport: innerWidth,
    document: document.documentElement.scrollWidth,
  }));
  expect(widths.document).toBeLessThanOrEqual(widths.viewport + 1);
  await page.screenshot({
    path: "test-results/orders-current-state-mobile.png",
    fullPage: true,
  });
  const rejected = await page.request.post(
    `${origin}/__test/orders/merchant-reconcile?reject_native=403`,
    { headers: { "X-Orders-Test-Control": control } },
  );
  expect(rejected.status()).toBe(204);
  const documents = posting.locator("details").filter({
    has: page.locator("summary", {
      hasText: /^Отгрузка и возврат в МойСклад$/,
    }),
  });
  await documents.locator("summary").click();
  await documents
    .getByLabel(/^Количество ·/)
    .first()
    .fill("1");
  const planned = new Date(Date.now() + 3600000);
  await documents
    .getByLabel("Плановое время документа")
    .fill(
      new Date(planned.getTime() - planned.getTimezoneOffset() * 60000)
        .toISOString()
        .slice(0, -1),
    );
  await documents
    .getByLabel("Идентификатор планирования")
    .fill("browser-native-known-rejection");
  await documents
    .getByRole("button", { name: "Проверить документ перед созданием" })
    .click();
  const preview = page.locator(
    'section[aria-labelledby="orders-preview-heading"]',
  );
  async function confirmNative() {
    await preview
      .getByLabel("Подтвердить отправление position-http-posting", {
        exact: true,
      })
      .check();
    await preview
      .getByLabel(
        "Я проверил фактическое основание, состав и проведение документа и подтверждаю создание складского документа в МойСклад.",
        { exact: true },
      )
      .check();
    await preview
      .getByRole("button", { name: "Создать складские документы (1)" })
      .click();
  }
  await confirmNative();
  await expect(posting).toContainText("Последняя операция: Отклонено", {
    timeout: 25000,
  });
  if ((await documents.getAttribute("open")) === null)
    await documents.locator("summary").click();
  await documents.getByLabel("Подготовить повтор после отказа API").check();
  await expect(documents.getByRole("option", { name: /HTTP 403/ })).toHaveCount(
    1,
  );
  await documents
    .getByLabel("Операция для проверки повторной попытки")
    .selectOption({
      label: await documents
        .getByRole("option", { name: /HTTP 403/ })
        .innerText(),
    });
  await expect(documents.getByLabel("Идентификатор планирования")).toHaveCount(
    0,
  );
  await documents
    .getByRole("button", { name: "Проверить документ перед созданием" })
    .click();
  await expect(preview).toContainText(
    "Новая попытка после подтверждённого отказа без применения",
  );
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.screenshot({
    path: "test-results/orders-native-retry-desktop.png",
    fullPage: true,
  });
  await page.setViewportSize({ width: 390, height: 844 });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth + 1,
    ),
  ).toBe(true);
  await page.screenshot({
    path: "test-results/orders-native-retry-mobile.png",
    fullPage: true,
  });
  await confirmNative();
  await expect(posting).toContainText(
    "Последняя операция: Подтверждено сверкой",
    { timeout: 25000 },
  );
  expect(errors).toEqual([]);
});
