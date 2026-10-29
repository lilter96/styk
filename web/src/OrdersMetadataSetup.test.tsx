import { afterEach, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { OrdersConfig } from "./OrdersConfig";
import {
  orderServiceFields,
  type OrdersMetadataRegistry,
} from "./OrdersMetadataSetup";
import {
  orderScopeBody,
  type OrderScope,
  type OrdersConfigData,
} from "./OrdersTypes";

const scope: OrderScope = {
  tenant: "setup-synthetic",
  moy: "moy-synthetic",
  ozon: "ozon-synthetic",
};
const ids = [
  "10000000-0000-0000-0000-000000000001",
  "10000000-0000-0000-0000-000000000002",
  "10000000-0000-0000-0000-000000000003",
];
const configuration = (
  currentScope = scope,
  available = true,
): OrdersConfigData => ({
  scope: orderScopeBody(currentScope),
  revision: 1,
  enabled: false,
  test_enabled: false,
  publication_available: available,
  interval_seconds: 300,
  source_freshness_seconds: 300,
  preview_ttl_seconds: 300,
  pii_enabled: false,
  pii_retention_seconds: 0,
  organization_id: "",
  agent_id: "",
  store_id: "",
  state_id: "",
  sales_channel_id: "",
  currency_id: "",
  field_mappings: [{ source: "posting_number", target: "name" }],
  status_mappings: [],
});
const registry = (count = 0, currentScope = scope): OrdersMetadataRegistry => ({
  scope: orderScopeBody(currentScope),
  revision: count,
  attributes: orderServiceFields.slice(0, count).map((field, index) => ({
    unique_code: field.unique_code,
    attribute_id: ids[index],
    type: "string",
  })),
});
const json = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  });
function mockAPI(
  options: {
    ready?: number;
    fieldResponse?: (
      code: string,
      body: Record<string, unknown>,
    ) => Response | undefined;
  } = {},
) {
  let current = registry(options.ready);
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    const url = new URL(path, "http://localhost");
    if (url.pathname.endsWith("/metadata/registry"))
      return json({
        ...current,
        scope: {
          tenant: url.searchParams.get("tenant"),
          moysklad_account: url.searchParams.get("moysklad_account"),
          ozon_account: url.searchParams.get("ozon_account"),
        },
      });
    if (url.pathname.endsWith("/metadata/attributes")) {
      const body = JSON.parse(init!.body as string);
      const custom = options.fieldResponse?.(body.unique_code, body);
      if (custom) return custom;
      const index = orderServiceFields.findIndex(
        (field) => field.unique_code === body.unique_code,
      );
      current = {
        ...current,
        revision: current.revision + 1,
        attributes: [
          ...current.attributes,
          {
            unique_code: body.unique_code,
            attribute_id: ids[index],
            type: "string",
          },
        ],
      };
      return json({
        state: "RECONCILED",
        registry: current,
        intent_id: `intent-${index}`,
      });
    }
    if (url.pathname.endsWith("/metadata"))
      return json({
        scope: {
          tenant: url.searchParams.get("tenant"),
          moysklad_account: url.searchParams.get("moysklad_account"),
          ozon_account: url.searchParams.get("ozon_account"),
        },
        organizations: [],
        agents: [],
        stores: [],
        states: [],
        sales_channels: [],
        currencies: [],
        attributes: [],
      });
    if (url.pathname.endsWith("/config") && init?.method === "POST")
      return json({ ...JSON.parse(init.body as string).config, revision: 2 });
    throw new Error(`Unexpected setup fixture endpoint ${url.pathname}`);
  });
  vi.stubGlobal("fetch", fetcher);
  return {
    fetcher,
    setRegistry: (next: OrdersMetadataRegistry) => {
      current = next;
    },
  };
}
function mount(currentScope = scope, available = true) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  const onSaved = vi.fn();
  const rendered = render(
    <QueryClientProvider client={client}>
      <OrdersConfig
        key={JSON.stringify(currentScope)}
        scope={currentScope}
        config={configuration(currentScope, available)}
        moyLabel="Тестовый аккаунт"
        onSaved={onSaved}
        onDirty={() => {}}
      />
    </QueryClientProvider>,
  );
  return { ...rendered, onSaved };
}
const creationPosts = (fetcher: ReturnType<typeof mockAPI>["fetcher"]) =>
  fetcher.mock.calls.filter(
    ([path, init]) =>
      path.endsWith("/metadata/attributes") && init?.method === "POST",
  );
const createButton = () =>
  screen.getByRole("button", {
    name: "Создать служебные поля Ozon в МойСклад",
  });
async function acknowledge() {
  await waitFor(() =>
    expect(
      (
        screen.getByLabelText(
          /Разрешаю создать отсутствующие служебные поля/,
        ) as HTMLInputElement
      ).disabled,
    ).toBe(false),
  );
  fireEvent.click(
    screen.getByLabelText(/Разрешаю создать отсутствующие служебные поля/),
  );
}
afterEach(() => {
  cleanup();
  sessionStorage.clear();
  vi.unstubAllGlobals();
});

it("only reads on view and requires explicit account-targeted consent before sequential creation", async () => {
  const { fetcher } = mockAPI();
  const { onSaved } = mount();
  await screen.findByText("ID заказа Ozon");
  expect(creationPosts(fetcher)).toHaveLength(0);
  expect((createButton() as HTMLButtonElement).disabled).toBe(true);
  await acknowledge();
  fireEvent.click(createButton());
  await waitFor(() =>
    expect(screen.getAllByText("Подтверждено в МойСклад")).toHaveLength(3),
  );
  const bodies = creationPosts(fetcher).map(([, init]) =>
    JSON.parse(init!.body as string),
  );
  expect(bodies.map((body) => body.unique_code)).toEqual(
    orderServiceFields.map((field) => field.unique_code),
  );
  bodies.forEach((body) =>
    expect(body).toEqual({
      ...orderScopeBody(scope),
      unique_code: expect.any(String),
      type: "string",
      idempotency_key: expect.any(String),
      acknowledge: true,
    }),
  );
  expect(new Set(bodies.map((body) => body.idempotency_key)).size).toBe(3);
  expect(onSaved).not.toHaveBeenCalled();
  expect(
    fetcher.mock.calls.filter(
      ([path]) => path.endsWith("/config") || path.endsWith("/approve"),
    ),
  ).toHaveLength(0);
  expect(
    (
      screen.getByLabelText(
        "Переносить ID и статусы в служебные поля",
      ) as HTMLInputElement
    ).checked,
  ).toBe(false);
});

it("hides creation when publication is unavailable and still allows read-only registry refresh", async () => {
  const { fetcher } = mockAPI();
  mount(scope, false);
  await screen.findByText("ID заказа Ozon");
  expect(
    screen.queryByRole("button", {
      name: "Создать служебные поля Ozon в МойСклад",
    }),
  ).toBeNull();
  const refresh = screen.getByRole("button", {
    name: "Обновить сведения о служебных полях",
  }) as HTMLButtonElement;
  await waitFor(() => expect(refresh.disabled).toBe(false));
  fireEvent.click(refresh);
  await waitFor(() =>
    expect(
      fetcher.mock.calls.filter(([path]) => path.includes("metadata/registry"))
        .length,
    ).toBeGreaterThan(1),
  );
  expect(creationPosts(fetcher)).toHaveLength(0);
});

it("adds only reconciled registry UUIDs after explicit mapping selection and separate config save", async () => {
  const { fetcher } = mockAPI({ ready: 3 });
  mount();
  const transfer = screen.getByLabelText(
    "Переносить ID и статусы в служебные поля",
  ) as HTMLInputElement;
  await waitFor(() => expect(transfer.disabled).toBe(false));
  expect(transfer.checked).toBe(false);
  fireEvent.click(transfer);
  expect(
    fetcher.mock.calls.filter(([, init]) => init?.method === "POST"),
  ).toHaveLength(0);
  fireEvent.click(
    screen.getByRole("button", { name: "Сохранить настройки заказов" }),
  );
  await waitFor(() =>
    expect(
      fetcher.mock.calls.filter(([path]) => path.endsWith("/config")),
    ).toHaveLength(1),
  );
  const body = JSON.parse(
    fetcher.mock.calls.find(([path]) => path.endsWith("/config"))![1]!
      .body as string,
  );
  expect(body.config.field_mappings).toEqual([
    { source: "posting_number", target: "name" },
    ...orderServiceFields.map((field, index) => ({
      source: field.source,
      target: "attribute",
      attribute_id: ids[index],
    })),
  ]);
  expect(creationPosts(fetcher)).toHaveLength(0);
});

it("persists an unknown creation intent across remount and never repeats the write during registry refresh", async () => {
  const { fetcher } = mockAPI({
    fieldResponse: () => json({ error: "UNAVAILABLE" }, 503),
  });
  const initial = mount();
  await acknowledge();
  fireEvent.click(createButton());
  await screen.findByText(/Результат создания неизвестен:/);
  expect(creationPosts(fetcher)).toHaveLength(1);
  const first = JSON.parse(creationPosts(fetcher)[0][1]!.body as string);
  initial.unmount();
  mount();
  await screen.findByText(/Результат создания неизвестен:/);
  const refresh = screen.getByRole("button", {
    name: "Обновить сведения о служебных полях",
  }) as HTMLButtonElement;
  await waitFor(() => expect(refresh.disabled).toBe(false));
  fireEvent.click(refresh);
  expect((createButton() as HTMLButtonElement).disabled).toBe(true);
  expect(creationPosts(fetcher)).toHaveLength(1);
  expect(JSON.stringify(sessionStorage)).toContain(first.idempotency_key);
});

it.each(["provider-code", "http403"])(
  "shows partial results and per-field permission errors without claiming all fields succeeded (%s)",
  async (failure) => {
    const { fetcher } = mockAPI({
      fieldResponse: (code) =>
        code === "styk-order-status"
          ? failure === "http403"
            ? json({ error: "FORBIDDEN" }, 403)
            : json({
                state: "REJECTED",
                code: "PROVIDER_ADMIN_REQUIRED",
                registry: { ...registry(1) },
              })
          : undefined,
    });
    mount();
    await acknowledge();
    fireEvent.click(createButton());
    await screen.findByText(/Недостаточно прав для создания поля/);
    await waitFor(() => expect(creationPosts(fetcher)).toHaveLength(3));
    expect(screen.getAllByText("Подтверждено в МойСклад")).toHaveLength(2);
    expect(screen.getByText("Недостаточно прав", { exact: true })).toBeTruthy();
    expect(
      (
        screen.getByLabelText(
          "Переносить ID и статусы в служебные поля",
        ) as HTMLInputElement
      ).disabled,
    ).toBe(true);
    expect(
      fetcher.mock.calls.filter(([path]) => path.endsWith("/config")),
    ).toHaveLength(0);
  },
);

it("resets consent and unknown intent presentation for another account without leaking the old scope", async () => {
  const { fetcher } = mockAPI({
    fieldResponse: () => json({ error: "UNAVAILABLE" }, 503),
  });
  const initial = mount();
  await acknowledge();
  fireEvent.click(createButton());
  await screen.findByText(/Результат создания неизвестен:/);
  initial.unmount();
  const next = { ...scope, moy: "another-moy-synthetic" };
  mount(next);
  await waitFor(() =>
    expect(
      (
        screen.getByLabelText(
          /Разрешаю создать отсутствующие служебные поля/,
        ) as HTMLInputElement
      ).disabled,
    ).toBe(false),
  );
  expect(screen.queryByText(/Результат создания неизвестен:/)).toBeNull();
  expect((createButton() as HTMLButtonElement).disabled).toBe(true);
  expect(creationPosts(fetcher)).toHaveLength(1);
});
