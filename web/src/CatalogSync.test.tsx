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
import { makeCatalogFacts } from "./CatalogSyncFacts";
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
  enabled: true,
  test_enabled: true,
  publication_available: true,
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
  expect(screen.getByRole("cell", { name: "Неизвестно" })).toBeTruthy();
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
              id: "7:8",
              name: "Посуда",
              attributes: [
                { id: "101", name: "Вес", required: true, type: "number" },
                {
                  id: "102",
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
  fireEvent.change(
    await screen.findByLabelText("Категория товара в Ozon", { exact: true }),
    {
      target: { value: "7:8" },
    },
  );
  expect(changed).toHaveBeenLastCalledWith(
    expect.objectContaining({
      category_id: "7:8",
      attributes: {},
    }),
  );
  const weight = screen.getByLabelText(/Вес/);
  const fragile = screen.getByLabelText(/Хрупкий товар/);
  expect((weight as HTMLInputElement).value).toBe("");
  expect((fragile as HTMLSelectElement).value).toBe("");
  fireEvent.change(weight, { target: { value: "0" } });
  expect(changed).toHaveBeenLastCalledWith(
    expect.objectContaining({
      category_id: "7:8",
      attributes: { "101": "0" },
    }),
  );
  fireEvent.change(fragile, { target: { value: "false" } });
  expect(changed).toHaveBeenLastCalledWith(
    expect.objectContaining({
      category_id: "7:8",
      attributes: { "102": "false" },
    }),
  );
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

it("creates an unmapped Ozon product in MoySklad only through a durable explicit enrollment and review", async () => {
  const source: CatalogProduct = {
    ...product("unmapped-ozon", "Новый товар Ozon"),
    identity: identity("unmapped-ozon", "ozon"),
    fields: {
      name: { state: "known", data: "Новый товар Ozon" },
      description: { state: "known", data: "Описание из источника" },
      price: { state: "known", data: "99" },
      category: { state: "unknown" },
    },
  };
  let saved = configuration({ targets: [] });
  const fetcher = mockAPI((url, init) => {
    if (url.pathname.endsWith("/config")) {
      if (init?.method === "POST")
        saved = {
          ...JSON.parse(init.body as string).config,
          revision: saved.revision + 1,
        };
      return json(saved);
    }
    if (url.pathname.endsWith("/products"))
      return json({
        products: url.searchParams.get("provider") === "ozon" ? [source] : [],
        enrollments: [],
      });
    if (url.pathname.endsWith("/preview")) {
      const creation = saved.targets[0];
      return json({
        ...preview(),
        config_revision: saved.revision,
        decisions: [
          decision({
            enrollment_id: creation.id,
            source,
            target: { identity: creation.target, fields: {}, revision: "" },
            target_absent: true,
            commands: [
              {
                action: "create",
                target: creation.target,
                values: creation.create!.values,
              },
            ],
            plan: {
              mapping_id: "",
              mapping_version: 0,
              fingerprint: "creation-fingerprint",
              changes: [],
              conflicts: [],
              skipped: [],
              noop: false,
            },
          }),
        ],
      });
    }
    return undefined;
  });
  mount();
  fireEvent.change(await screen.findByLabelText("Список товаров"), {
    target: { value: "ozon" },
  });
  fireEvent.click(
    await screen.findByRole("button", { name: "Создать в МойСклад" }),
  );
  const name = screen.getByLabelText(
    "Название нового товара",
  ) as HTMLInputElement;
  expect(name.value).toBe("Новый товар Ozon");
  expect(
    screen.queryByText("Категория Ozon и фактические характеристики"),
  ).toBeNull();
  expect(
    fetcher.mock.calls.filter(([, init]) => init?.method === "POST"),
  ).toHaveLength(0);
  fireEvent.click(
    screen.getByRole("button", { name: "Проверить выбранные товары" }),
  );
  await screen.findByRole("heading", {
    name: "Проверьте изменения перед отправкой",
  });
  const created = saved.targets[0];
  expect(created.id).toMatch(/^create-[\da-f-]{36}$/);
  expect(created.create?.id).toMatch(/^[\da-f-]{36}$/);
  expect(created.create?.external_code).toBe(
    `STYK-CATALOG-${created.create!.id}`,
  );
  expect(created.target).toEqual({
    id: "",
    entity_type: "product",
    account: { provider: "moysklad", id: scope.moy },
  });
  expect(created.mapping_id).toBeUndefined();
  expect(created.automatic).toBe(false);
  expect(created.create?.values).toEqual({
    name: { state: "known", data: "Новый товар Ozon" },
    description: { state: "known", data: "Описание из источника" },
  });
  expect(screen.getByText("Будет создан новый товар")).toBeTruthy();
  expect(
    fetcher.mock.calls.filter(([url]) => String(url).includes("/approve")),
  ).toHaveLength(0);
  fireEvent.click(
    screen.getByLabelText("Подтвердить изменения товара Новый товар Ozon"),
  );
  fireEvent.click(
    screen.getByLabelText(/Я проверил изменения выбранных товаров/),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Отправить изменения (1)" }),
  );
  await screen.findByText(/Подтверждение записано/);
  const approval = fetcher.mock.calls.find(([url]) =>
    String(url).includes("/approve"),
  )!;
  expect(JSON.parse(approval[1]!.body as string).enrollment_ids).toEqual([
    created.id,
  ]);
});

it("joins an Ozon row to the target side of a canonical mapping without disabling its automatic allowlist", async () => {
  const configured = enrollment();
  configured.automatic = true;
  configured.automatic_fields = ["name"];
  const fetcher = mockAPI((url) => {
    if (url.pathname.endsWith("/config"))
      return json(configuration({ targets: [configured] }));
    if (url.pathname.endsWith("/products"))
      return json({
        products: url.searchParams.get("provider") === "ozon" ? [target] : [],
        enrollments: [enrollment()],
      });
    return undefined;
  });
  mount();
  fireEvent.change(await screen.findByLabelText("Список товаров"), {
    target: { value: "ozon" },
  });
  const choice = (await screen.findByLabelText(
    "Выбрать товар Чашка",
  )) as HTMLInputElement;
  expect(choice.disabled).toBe(false);
  fireEvent.click(choice);
  fireEvent.click(
    screen.getByRole("button", { name: "Проверить выбранные товары" }),
  );
  await screen.findByRole("heading", {
    name: "Проверьте изменения перед отправкой",
  });
  expect(
    fetcher.mock.calls.filter(
      ([url, init]) =>
        String(url).endsWith("/config") && init?.method === "POST",
    ),
  ).toHaveLength(0);
});

it("loads only the selected category schema and explicitly requested official dictionary pages", async () => {
  const category = {
    id: "7:8",
    name: "Посуда",
    attributes: [],
    attributes_loaded: false,
  };
  const attribute = {
    id: "123",
    name: "Материал",
    required: true,
    type: "string",
    dictionary: true,
    options_incomplete: true,
  };
  const fetcher = mockAPI((url) => {
    if (url.pathname.endsWith("/categories"))
      return url.searchParams.get("category_id")
        ? json({
            ...category,
            attributes: [attribute],
            attributes_loaded: true,
          })
        : json({ items: [category] });
    if (url.pathname.endsWith("/attributes"))
      return url.searchParams.get("cursor")
        ? json({ items: [{ id: "12", name: "Керамика" }] })
        : json({ items: [{ id: "11", name: "Фарфор" }], next_cursor: "11" });
    return undefined;
  });
  const changed = vi.fn();
  mount(<CatalogSyncFacts scope={scope} onChange={changed} />);
  fireEvent.click(
    screen.getByText("Категория Ozon и фактические характеристики"),
  );
  fireEvent.change(
    await screen.findByLabelText("Категория товара в Ozon", { exact: true }),
    {
      target: { value: "7:8" },
    },
  );
  const input = await screen.findByLabelText("Материал (обязательно)");
  expect(screen.queryByText(/нет обязательных характеристик/)).toBeNull();
  expect(
    fetcher.mock.calls.filter(([url]) => String(url).includes("/attributes?")),
  ).toHaveLength(0);
  fireEvent.click(
    screen.getByRole("button", { name: "Загрузить значения справочника" }),
  );
  await screen.findByRole("option", { name: "Фарфор" });
  fireEvent.change(input, { target: { value: "11" } });
  const facts = changed.mock.calls[changed.mock.calls.length - 1][0];
  expect(facts.values.category).toEqual({
    state: "known",
    data: { description_category_id: 7, type_id: 8 },
  });
  expect(facts.values.attributes).toEqual({
    state: "known",
    data: [
      {
        id: 123,
        complex_id: 0,
        values: [{ dictionary_value_id: 11, value: "Фарфор" }],
      },
    ],
  });
  fireEvent.click(screen.getByRole("button", { name: "Следующие значения" }));
  await screen.findByRole("option", { name: "Керамика" });
  expect(
    fetcher.mock.calls.some(
      ([url]) =>
        String(url).includes("attribute_id=123") &&
        String(url).includes("cursor=11"),
    ),
  ).toBe(true);
});

it("does not turn unverified dictionary text into a factual attribute", () => {
  const facts = makeCatalogFacts(
    {
      id: "7:8",
      name: "Посуда",
      attributes: [
        {
          id: "123",
          name: "Материал",
          required: true,
          type: "select",
          dictionary: true,
        },
      ],
    },
    { "123": "Придуманное значение" },
  );
  expect(facts.values?.attributes).toEqual({ state: "known", data: [] });
});

it("keeps publication unavailable even if a preview incorrectly advertises a ready decision", async () => {
  const fetcher = mockAPI((url) =>
    url.pathname.endsWith("/config")
      ? json(configuration({ publication_available: false }))
      : undefined,
  );
  mount();
  await review();
  expect(
    screen.getByText(
      /Отправка изменений в сервисы для этого аккаунта пока недоступна/,
    ),
  ).toBeTruthy();
  expect(
    (
      screen.getByLabelText(
        "Подтвердить изменения товара Тестовая чашка",
      ) as HTMLInputElement
    ).disabled,
  ).toBe(true);
  expect(
    fetcher.mock.calls.filter(([url]) => String(url).includes("/approve")),
  ).toHaveLength(0);
});

it("saves explicit Ozon category facts into creation values and removes them when the seller clears the category", async () => {
  let saved = configuration({ targets: [] });
  const fetcher = mockAPI((url, init) => {
    if (url.pathname.endsWith("/config")) {
      if (init?.method === "POST")
        saved = {
          ...JSON.parse(init.body as string).config,
          revision: saved.revision + 1,
        };
      return json(saved);
    }
    if (url.pathname.endsWith("/products"))
      return json({ products: [product()], enrollments: [] });
    if (url.pathname.endsWith("/categories")) {
      const category = {
        id: "7:8",
        name: "Посуда",
        attributes_loaded: true,
        attributes: [
          {
            id: "101",
            name: "Вес для категории",
            required: true,
            type: "number",
          },
        ],
      };
      return json(
        url.searchParams.get("category_id") ? category : { items: [category] },
      );
    }
    if (url.pathname.endsWith("/preview"))
      return json({
        ...preview([
          decision({ ready: false, validation: [{ code: "VAT_REQUIRED" }] }),
        ]),
        config_revision: saved.revision,
      });
    return undefined;
  });
  mount();
  fireEvent.click(
    await screen.findByRole("button", { name: "Создать в Ozon" }),
  );
  fireEvent.change(screen.getByLabelText("Артикул нового товара в Ozon"), {
    target: { value: "SYNTHETIC-NEW-OFFER" },
  });
  expect(
    (screen.getByLabelText("Цена нового товара") as HTMLInputElement).value,
  ).toBe("");
  expect(
    (screen.getByLabelText("НДС для Ozon") as HTMLInputElement).value,
  ).toBe("");
  fireEvent.click(
    screen.getByText("Категория Ozon и фактические характеристики"),
  );
  fireEvent.change(
    await screen.findByLabelText("Категория товара в Ozon", { exact: true }),
    {
      target: { value: "7:8" },
    },
  );
  fireEvent.change(
    await screen.findByLabelText("Вес для категории (обязательно)"),
    { target: { value: "0" } },
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Проверить выбранные товары" }),
  );
  await screen.findByText("Товар: Укажите фактическую ставку НДС для Ozon.");
  const originalID = saved.targets[0].create!.id;
  expect(saved.targets[0].create!.values.category).toEqual({
    state: "known",
    data: { description_category_id: 7, type_id: 8 },
  });
  expect(saved.targets[0].create!.values.attributes).toEqual({
    state: "known",
    data: [
      {
        id: 101,
        complex_id: 0,
        values: [{ dictionary_value_id: 0, value: "0" }],
      },
    ],
  });
  expect(saved.targets[0].create!.values.price).toBeUndefined();
  expect(saved.targets[0].create!.values.currency).toBeUndefined();
  expect(saved.targets[0].create!.extensions).toBeUndefined();
  fireEvent.click(
    screen.getByRole("button", {
      name: "Убрать категорию и введённые характеристики",
    }),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Проверить выбранные товары" }),
  );
  await screen.findByText("Товар: Укажите фактическую ставку НДС для Ozon.");
  expect(saved.targets).toHaveLength(1);
  expect(saved.targets[0].create!.id).toBe(originalID);
  expect(saved.targets[0].create!.values.category).toBeUndefined();
  expect(saved.targets[0].create!.values.attributes).toBeUndefined();
  expect(
    fetcher.mock.calls.filter(([url]) => String(url).includes("/approve")),
  ).toHaveLength(0);
});

it.each([true, false])(
  "enables controlled catalog work only after explicit saving and server availability=%s",
  async (available) => {
    const fetcher = mockAPI((url, init) => {
      if (!url.pathname.endsWith("/config")) return undefined;
      if (init?.method === "POST")
        return json({ ...JSON.parse(init.body as string).config, revision: 2 });
      return json(
        configuration({
          enabled: false,
          test_enabled: false,
          publication_available: available,
        }),
      );
    });
    mount();
    fireEvent.click(
      await screen.findByLabelText(
        "Разрешить синхронизацию каталога для выбранных аккаунтов",
      ),
    );
    expect(
      fetcher.mock.calls.filter(([, init]) => init?.method === "POST"),
    ).toHaveLength(0);
    fireEvent.click(
      screen.getByRole("button", { name: "Сохранить настройки" }),
    );
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
      ([, init]) => init?.method === "POST",
    )!;
    const body = JSON.parse(save[1]!.body as string);
    expect(body.config.enabled).toBe(true);
    expect(body.config.test_enabled).toBe(available);
    expect(body.config.publication_available).toBeUndefined();
    if (!available)
      expect(
        screen.getByText(
          /Отправка изменений в сервисы для этого аккаунта пока недоступна/,
        ),
      ).toBeTruthy();
  },
);
