import { afterEach, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { CatalogSync } from "./CatalogSync";
import { CatalogSyncPreview } from "./CatalogSyncPreview";
import { CatalogSyncFacts } from "./CatalogSyncFacts";
import {
  displayValue,
  scopeBody,
  type CatalogConfig,
  type CatalogOperation,
  type CatalogPreview,
  type CatalogProduct,
  type Decision,
  type Enrollment,
  type Scope,
} from "./CatalogSyncTypes";

const scope: Scope = {
  tenant: "synthetic-account",
  moy: "synthetic-moy",
  ozon: "synthetic-ozon",
};
const identity = (id: string, provider: "moysklad" | "ozon" = "moysklad") => ({
  id,
  account: { provider, id: provider === "moysklad" ? scope.moy : scope.ozon },
});
const product = (id = "source-1", name = "Тестовая чашка"): CatalogProduct => ({
  identity: identity(id),
  revision: "source-v1",
  fields: { name: { state: "known", data: name } },
  lifecycle: "active",
});
const target = {
  ...product("target-1", "Чашка"),
  identity: identity("target-1", "ozon"),
};
const enrollment = (id = "enrollment-1", source = product()): Enrollment => ({
  id,
  mapping_id: `mapping-${id}`,
  mapping_version: 1,
  source: source.identity,
  target: target.identity,
  automatic: false,
});
const configuration = (extra: Partial<CatalogConfig> = {}): CatalogConfig => ({
  scope: scopeBody(scope),
  revision: 1,
  enabled: false,
  test_enabled: false,
  interval_seconds: 300,
  rules: { name: "MOYSKLAD_AUTHORITATIVE", description: "OZON_AUTHORITATIVE" },
  targets: [enrollment()],
  updated_at: "2026-10-29T12:00:00Z",
  ...extra,
});
const decision = (extra: Partial<Decision> = {}): Decision => ({
  enrollment_id: "enrollment-1",
  source: product(),
  target,
  target_absent: false,
  ready: true,
  validation: [],
  commands: [
    {
      action: "update",
      target: target.identity,
      values: { name: { state: "known", data: "Тестовая чашка" } },
    },
  ],
  plan: {
    mapping_id: "mapping-1",
    mapping_version: 1,
    fingerprint: "reviewed-fingerprint",
    changes: [
      {
        field: "name",
        source: product().identity,
        target: target.identity,
        before: { state: "known", data: "Чашка" },
        after: { state: "known", data: "Тестовая чашка" },
      },
    ],
    conflicts: [],
    skipped: [],
    noop: false,
  },
  ...extra,
});
const preview = (decisions = [decision()]): CatalogPreview => ({
  id: "preview-1",
  scope: scopeBody(scope),
  config_revision: 1,
  expires_at: new Date(Date.now() + 60000).toISOString(),
  decisions,
});
const operation = (state = "QUEUED"): CatalogOperation => ({
  id: "operation-1",
  scope: scopeBody(scope),
  preview_id: "preview-1",
  state,
  created_at: "2026-10-29T12:00:00Z",
  targets: [
    {
      id: "work-1",
      enrollment_id: "enrollment-1",
      state,
      decision: decision(),
      command: decision().commands[0],
      receipt: {
        state: state === "PROCESSING" ? "accepted" : "unknown",
        task_id: state === "PROCESSING" ? "task-synthetic" : undefined,
      },
    },
  ],
});
const json = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  });
type Handler = (
  path: URL,
  init?: RequestInit,
) => Response | Promise<Response> | undefined;
function mockAPI(handler?: Handler) {
  return vi
    .spyOn(globalThis, "fetch")
    .mockImplementation(async (input, init) => {
      const url = new URL(String(input), "http://localhost");
      const custom = handler?.(url, init);
      if (custom !== undefined) return custom;
      if (url.pathname.endsWith("/config")) return json(configuration());
      if (url.pathname.endsWith("/products"))
        return json({ products: [product()], enrollments: [enrollment()] });
      if (url.pathname.endsWith("/preview")) return json(preview());
      if (url.pathname.endsWith("/approve")) return json(operation());
      if (url.pathname.endsWith("/operations")) return json({ operations: [] });
      return json({ items: [] });
    });
}
const clients: QueryClient[] = [];
function mount(node = <CatalogSync scope={scope} />) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  clients.push(client);
  return render(
    <QueryClientProvider client={client}>{node}</QueryClientProvider>,
  );
}
async function review() {
  fireEvent.click(await screen.findByLabelText("Выбрать товар Тестовая чашка"));
  fireEvent.click(
    screen.getByRole("button", { name: "Проверить выбранные товары" }),
  );
  return screen.findByRole("heading", {
    name: "Проверьте изменения перед отправкой",
  });
}
async function confirm() {
  fireEvent.click(
    screen.getByLabelText("Подтвердить изменения товара Тестовая чашка"),
  );
  fireEvent.click(
    screen.getByLabelText(/Я проверил изменения выбранных товаров/),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Отправить изменения (1)" }),
  );
}
afterEach(() => {
  cleanup();
  clients.forEach((client) => client.clear());
  clients.length = 0;
  vi.restoreAllMocks();
});

it("previews without approving and submits only explicitly selected canonical enrollment IDs", async () => {
  const fetcher = mockAPI();
  mount();
  await review();
  const submit = screen.getByRole("button", {
    name: "Отправить изменения (0)",
  }) as HTMLButtonElement;
  expect(submit.disabled).toBe(true);
  expect(
    fetcher.mock.calls.filter(([url]) => String(url).includes("/approve")),
  ).toHaveLength(0);
  fireEvent.click(
    screen.getByLabelText("Подтвердить изменения товара Тестовая чашка"),
  );
  expect(
    (
      screen.getByRole("button", {
        name: "Отправить изменения (1)",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  fireEvent.click(
    screen.getByLabelText(/Я проверил изменения выбранных товаров/),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Отправить изменения (1)" }),
  );
  expect(await screen.findByText(/Подтверждение записано/)).toBeTruthy();
  const approvals = fetcher.mock.calls.filter(([url]) =>
    String(url).includes("/approve"),
  );
  expect(approvals).toHaveLength(1);
  expect(JSON.parse(approvals[0][1]!.body as string)).toEqual({
    ...scopeBody(scope),
    preview_id: "preview-1",
    enrollment_ids: ["enrollment-1"],
    acknowledge: true,
    idempotency_key: expect.any(String),
  });
  expect(screen.queryByText("Изменения подтверждены сверкой")).toBeNull();
});

it("never retries an approval with an unknown outcome, including double clicks and status refresh", async () => {
  const fetcher = mockAPI((url) =>
    url.pathname.endsWith("/approve")
      ? Promise.reject(new TypeError("lost reply"))
      : undefined,
  );
  mount();
  await review();
  fireEvent.click(
    screen.getByLabelText("Подтвердить изменения товара Тестовая чашка"),
  );
  fireEvent.click(
    screen.getByLabelText(/Я проверил изменения выбранных товаров/),
  );
  const button = screen.getByRole("button", {
    name: "Отправить изменения (1)",
  });
  fireEvent.click(button);
  fireEvent.click(button);
  expect(
    await screen.findByText(/Результат подтверждения неизвестен:/),
  ).toBeTruthy();
  await waitFor(() =>
    expect((button as HTMLButtonElement).disabled).toBe(true),
  );
  fireEvent.click(screen.getByRole("button", { name: "Обновить статусы" }));
  expect(
    fetcher.mock.calls.filter(([url]) => String(url).includes("/approve")),
  ).toHaveLength(1);
  expect(
    (
      screen.getByRole("button", {
        name: "Проверить выбранные товары",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
});

it("blocks the whole product on a conflict and does not expose provider error messages", () => {
  const d = decision({
    ready: true,
    validation: [
      {
        field: "price",
        code: "invalid_value",
        message: "PRIVATE_PROVIDER_RESPONSE",
      },
    ],
  });
  d.plan.conflicts = [
    {
      field: "name",
      reason: "both_sides_changed",
      source_value: { state: "known", data: "Название А" },
      target_value: { state: "known", data: "Название Б" },
    },
  ];
  const approve = vi.fn();
  mount(
    <CatalogSyncPreview
      preview={preview([d])}
      busy={false}
      blocked={false}
      onApprove={approve}
    />,
  );
  expect(
    (
      screen.getByLabelText(
        "Подтвердить изменения товара Тестовая чашка",
      ) as HTMLInputElement
    ).disabled,
  ).toBe(true);
  expect(screen.getByText("Название: конфликт")).toBeTruthy();
  expect(screen.queryByText("PRIVATE_PROVIDER_RESPONSE")).toBeNull();
  fireEvent.click(
    screen.getByRole("button", { name: "Отправить изменения (0)" }),
  );
  expect(approve).not.toHaveBeenCalled();
});

it("keeps zero, empty, explicit clearing and unknown values distinct", () => {
  expect(displayValue({ state: "known", data: 0 })).toBe("0");
  expect(displayValue({ state: "known", data: false })).toBe("Нет");
  expect(displayValue({ state: "unknown", data: 0 })).toBe("Неизвестно");
  expect(displayValue({ state: "empty" })).toBe("Пустое значение");
  expect(displayValue({ state: "clear" })).toBe("Очистить значение");
  expect(displayValue({ state: "absent" })).toBe("Нет данных");
  const d = decision();
  d.plan.changes = [
    {
      ...d.plan.changes[0],
      before: { state: "known", data: 0 },
      after: { state: "unknown" },
    },
  ];
  d.commands[0].values.name = { state: "unknown" };
  mount(
    <CatalogSyncPreview
      preview={preview([d])}
      busy={false}
      blocked={false}
      onApprove={vi.fn()}
    />,
  );
  expect(screen.getByRole("cell", { name: "0" })).toBeTruthy();
  expect(
    screen.getByRole("cell", { name: "Неизвестно" }),
  ).toBeTruthy();
  expect(
    (
      screen.getByLabelText(
        "Подтвердить изменения товара Тестовая чашка",
      ) as HTMLInputElement
    ).disabled,
  ).toBe(true);
});

it("paginates and searches on the server, preserving explicit selections between pages", async () => {
  const second = product("source-2", "Тестовая тарелка");
  const fetcher = mockAPI((url) => {
    if (!url.pathname.endsWith("/products")) return undefined;
    return url.searchParams.get("cursor") === "next-page"
      ? json({
          products: [second],
          enrollments: [enrollment("enrollment-2", second)],
        })
      : json({
          products: [product()],
          enrollments: [enrollment()],
          next_cursor: "next-page",
        });
  });
  mount();
  fireEvent.click(await screen.findByLabelText("Выбрать товар Тестовая чашка"));
  fireEvent.click(screen.getByRole("button", { name: "Следующие товары" }));
  fireEvent.click(
    await screen.findByLabelText("Выбрать товар Тестовая тарелка"),
  );
  expect(screen.getByText("Выбрано товаров: 2")).toBeTruthy();
  expect(
    fetcher.mock.calls.some(([url]) =>
      String(url).includes("cursor=next-page"),
    ),
  ).toBe(true);
  fireEvent.change(screen.getByLabelText("Поиск товаров"), {
    target: { value: "товар & вариант" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Найти товары" }));
  await waitFor(() =>
    expect(
      fetcher.mock.calls.some(
        ([url]) =>
          new URL(String(url), "http://localhost").searchParams.get(
            "search",
          ) === "товар & вариант",
      ),
    ).toBe(true),
  );
  expect(screen.getByText("Выбрано товаров: 2")).toBeTruthy();
});

it("saves mixed authority settings with expected revision and invalidates the previous preview", async () => {
  const fetcher = mockAPI((url, init) => {
    if (!url.pathname.endsWith("/config") || init?.method !== "POST")
      return undefined;
    const body = JSON.parse(init.body as string);
    return json(configuration({ ...body.config, revision: 2 }));
  });
  mount();
  await review();
  fireEvent.change(screen.getByLabelText("Название", { exact: true }), {
    target: { value: "OZON_AUTHORITATIVE" },
  });
  expect(
    screen.queryByRole("heading", {
      name: "Проверьте изменения перед отправкой",
    }),
  ).toBeNull();
  expect(
    (
      screen.getByRole("button", {
        name: "Проверить выбранные товары",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  fireEvent.click(screen.getByRole("button", { name: "Сохранить настройки" }));
  await waitFor(() =>
    expect(
      (
        screen.getByRole("button", {
          name: "Сохранить настройки",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true),
  );
  const save = fetcher.mock.calls.find(
    ([url, init]) => String(url).endsWith("/config") && init?.method === "POST",
  )!;
  const body = JSON.parse(save[1]!.body as string);
  expect(body.expected_revision).toBe(1);
  expect(body.config.rules).toEqual({
    name: "OZON_AUTHORITATIVE",
    description: "OZON_AUTHORITATIVE",
  });
  expect(body.config.scope).toEqual(scopeBody(scope));
  expect(body.config.targets[0].automatic).toBe(false);
});

it("discards stale approval previews when server evidence changed", async () => {
  const fetcher = mockAPI((url) =>
    url.pathname.endsWith("/approve")
      ? json({ error: "CATALOG_STALE" }, 409)
      : undefined,
  );
  mount();
  await review();
  await confirm();
  expect(
    await screen.findByText(/Нужна новая проверка перед отправкой/),
  ).toBeTruthy();
  expect(
    screen.queryByRole("heading", {
      name: "Проверьте изменения перед отправкой",
    }),
  ).toBeNull();
  expect(
    fetcher.mock.calls.filter(([url]) => String(url).includes("/approve")),
  ).toHaveLength(1);
});

it("does not approve an expired preview", () => {
  const value = preview();
  value.expires_at = new Date(Date.now() - 1000).toISOString();
  const approve = vi.fn();
  mount(
    <CatalogSyncPreview
      preview={value}
      busy={false}
      blocked={false}
      onApprove={approve}
    />,
  );
  expect(screen.getByText(/Срок проверки истёк/)).toBeTruthy();
  expect(
    (
      screen.getByLabelText(
        "Подтвердить изменения товара Тестовая чашка",
      ) as HTMLInputElement
    ).disabled,
  ).toBe(true);
  expect(approve).not.toHaveBeenCalled();
});

it("distinguishes async acceptance, unknown outcomes and reconciled work without a retry action", async () => {
  mockAPI((url) =>
    url.pathname.endsWith("/operations")
      ? json({
          operations: [
            operation("PROCESSING"),
            { ...operation("UNKNOWN"), id: "operation-2" },
            { ...operation("RECONCILED"), id: "operation-3" },
          ],
        })
      : undefined,
  );
  mount();
  const asyncWork = await screen.findByRole("region", {
    name: "Отправка operation-1",
  });
  expect(
    within(asyncWork).getByText(/Принятие запроса ещё не подтверждает/),
  ).toBeTruthy();
  expect(
    within(asyncWork).queryByText("Изменения подтверждены сверкой"),
  ).toBeNull();
  const unknown = screen.getByRole("region", { name: "Отправка operation-2" });
  expect(within(unknown).getByText(/Запрос мог выполниться/)).toBeTruthy();
  expect(within(unknown).queryByRole("button")).toBeNull();
  expect(
    within(
      screen.getByRole("region", { name: "Отправка operation-3" }),
    ).getAllByText("Изменения подтверждены сверкой").length,
  ).toBeGreaterThan(0);
});

it("does not invent required factual values, including false and zero", async () => {
  mockAPI((url) =>
    url.pathname.endsWith("/categories")
      ? json({
          items: [
            {
              id: "category-1",
              name: "Посуда",
              attributes: [
                { id: "weight", name: "Вес", required: true, type: "number" },
                {
                  id: "fragile",
                  name: "Хрупкий товар",
                  required: true,
                  type: "boolean",
                },
              ],
            },
          ],
        })
      : undefined,
  );
  const changed = vi.fn();
  mount(<CatalogSyncFacts scope={scope} onChange={changed} />);
  fireEvent.click(
    screen.getByText("Категория Ozon и фактические характеристики"),
  );
  fireEvent.change(await screen.findByLabelText("Категория", { exact: true }), {
    target: { value: "category-1" },
  });
  expect(changed).toHaveBeenLastCalledWith({
    category_id: "category-1",
    attributes: {},
  });
  const weight = screen.getByLabelText(/Вес/);
  const fragile = screen.getByLabelText(/Хрупкий товар/);
  expect((weight as HTMLInputElement).value).toBe("");
  expect((fragile as HTMLSelectElement).value).toBe("");
  fireEvent.change(weight, { target: { value: "0" } });
  expect(changed).toHaveBeenLastCalledWith({
    category_id: "category-1",
    attributes: { weight: "0" },
  });
  fireEvent.change(fragile, { target: { value: "false" } });
  expect(changed).toHaveBeenLastCalledWith({
    category_id: "category-1",
    attributes: { fragile: "false" },
  });
});

it("makes no requests without a complete account pair", () => {
  const fetcher = mockAPI();
  mount(<CatalogSync scope={{ ...scope, ozon: "" }} />);
  expect(screen.getByText(/Подключите МойСклад и Ozon/)).toBeTruthy();
  expect(fetcher).not.toHaveBeenCalled();
});

it("isolates a late preview response after changing the account pair", async () => {
  let resolvePreview!: (response: Response) => void;
  const pending = new Promise<Response>((resolve) => {
    resolvePreview = resolve;
  });
  const other: Scope = {
    tenant: "other-synthetic",
    moy: "other-moy",
    ozon: "other-ozon",
  };
  mockAPI((url) => {
    if (url.pathname.endsWith("/preview")) return pending;
    if (url.searchParams.get("tenant") === other.tenant) {
      if (url.pathname.endsWith("/config"))
        return json(configuration({ scope: scopeBody(other), targets: [] }));
      if (url.pathname.endsWith("/products")) return json({ products: [] });
    }
    return undefined;
  });
  const client = new QueryClient();
  clients.push(client);
  const tree = (value: Scope) => (
    <QueryClientProvider client={client}>
      <CatalogSync scope={value} />
    </QueryClientProvider>
  );
  const view = render(tree(scope));
  fireEvent.click(await screen.findByLabelText("Выбрать товар Тестовая чашка"));
  fireEvent.click(
    screen.getByRole("button", { name: "Проверить выбранные товары" }),
  );
  view.rerender(tree(other));
  await screen.findByText(/Товары не найдены/);
  resolvePreview(json(preview()));
  await waitFor(() =>
    expect(
      screen.queryByRole("heading", {
        name: "Проверьте изменения перед отправкой",
      }),
    ).toBeNull(),
  );
  expect(screen.getByText("Выбрано товаров: 0")).toBeTruthy();
});
