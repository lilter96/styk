import { afterEach, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { Integrations } from "./Integrations";
import type { Account, Report } from "./api";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});
const tenant = "synthetic-tenant";
const accounts: Account[] = [
  {
    source: "moysklad",
    account: "moy-account",
    registered_at: "2026-01-01T00:00:00Z",
  },
  {
    source: "ozon",
    account: "ozon-account",
    registered_at: "2026-01-01T00:00:00Z",
  },
];
const selected = { moy: "moy-account", ozon: "ozon-account" };
const batch = {
  present: true,
  completed: true,
  age: "2h",
  observed_at: "2026-01-01T00:00:00Z",
  counts: { products: 3, warehouses: 2, stock_rows: 0 },
};
const report: Report = {
  batches: { moysklad: batch, ozon: batch },
  checks: [],
  decision_status: "NOT_CHECKED",
  decision_reasons: [],
  publication: "DISABLED",
  mapping_counts: {},
};
function mount(props: Partial<React.ComponentProps<typeof Integrations>> = {}) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <Integrations tenant={tenant} accounts={[]} {...props} />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}
function settings(card: HTMLElement) {
  const summary = within(card).getByText("Настройки");
  fireEvent.click(summary);
  expect(summary.closest("details")?.open).toBe(true);
}
function setup(failure = "") {
  let verified = false,
    registered = false;
  const fetcher = vi
    .spyOn(globalThis, "fetch")
    .mockImplementation(async (input) => {
      const path = String(input);
      if (path.endsWith("/probe")) {
        verified = true;
        return new Response("{}", { status: 200 });
      }
      if (path.endsWith("/register")) {
        if (failure)
          return new Response(JSON.stringify({ error: failure }), {
            status: failure === "OWNERSHIP_CONFLICT" ? 409 : 503,
          });
        registered = true;
        return new Response('{"status":"REGISTERED"}', { status: 200 });
      }
      return new Response(
        JSON.stringify({
          providers: [
            {
              provider: "moysklad",
              account: "moy-account",
              status: verified ? "VERIFIED" : "CONFIGURED_NOT_VERIFIED",
              registration: registered ? "REGISTERED" : "NOT_REGISTERED",
              check: verified
                ? {
                    checked_at: new Date().toISOString(),
                    identity: "NOT_ESTABLISHED",
                  }
                : undefined,
            },
            {
              provider: "ozon",
              status: "NOT_CONFIGURED",
              registration: "NOT_CHECKED",
            },
          ],
        }),
        { status: 200 },
      );
    });
  mount();
  return fetcher;
}
async function moyCard() {
  return (await screen.findByRole("heading", { name: "МойСклад" })).closest(
    "article",
  )!;
}
async function verify(card: HTMLElement) {
  fireEvent.click(
    within(card).getByRole("button", { name: "Проверить подключение" }),
  );
  await within(card).findByText(/^Доступ проверен/);
}
it("keeps registration explicit and requires a recent connection check and ownership acknowledgement", async () => {
  const fetcher = setup();
  const card = await moyCard();
  expect(
    within(card).getByText("Данные доступа настроены · доступ не проверен"),
  ).toBeTruthy();
  expect(
    fetcher.mock.calls.some(
      ([p, init]) => String(p).endsWith("/probe") || init?.method === "POST",
    ),
  ).toBe(false);
  expect(within(card).getByText("Настройки").closest("details")?.open).toBe(
    false,
  );
  settings(card);
  const register = within(card).getByRole("button", {
    name: "Добавить аккаунт",
  }) as HTMLButtonElement;
  expect(register.disabled).toBe(true);
  fireEvent.click(within(card).getByLabelText(/Подтверждаю, что этот аккаунт/));
  expect(register.disabled).toBe(true);
  await verify(card);
  expect(
    within(card).getByText(
      /Принадлежность аккаунта не подтверждена независимо/,
    ),
  ).toBeTruthy();
  fireEvent.click(register);
  await within(card).findByText("Аккаунт добавлен");
  const calls = fetcher.mock.calls.filter(([p]) =>
    String(p).endsWith("/register"),
  );
  expect(calls).toHaveLength(1);
  expect(JSON.parse(String(calls[0][1]?.body))).toEqual({
    tenant,
    acknowledge: true,
  });
  expect(
    within(card).getByRole("link", { name: "Товары" }).getAttribute("href"),
  ).toBe("/products");
  expect(
    within(card).getByRole("link", { name: "Склады" }).getAttribute("href"),
  ).toBe("/warehouses");
  expect(
    within(card)
      .getByRole("link", { name: "Диагностика" })
      .getAttribute("href"),
  ).toBe("/diagnostics");
  const ozon = screen
    .getByRole("heading", { name: "Ozon" })
    .closest("article")!;
  settings(ozon);
  expect(
    (
      within(ozon).getByRole("button", {
        name: "Проверить подключение",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
});
it("reconciles an unknown registration outcome before allowing another attempt", async () => {
  const fetcher = setup("REGISTRATION_OUTCOME_UNKNOWN");
  const card = await moyCard();
  settings(card);
  await verify(card);
  fireEvent.click(within(card).getByLabelText(/Подтверждаю, что этот аккаунт/));
  fireEvent.click(
    within(card).getByRole("button", { name: "Добавить аккаунт" }),
  );
  await screen.findByText(/Результат добавления неизвестен/);
  expect(
    (
      within(card).getByRole("button", {
        name: "Добавить аккаунт",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  expect(
    fetcher.mock.calls.filter(([p]) => String(p).endsWith("/register")),
  ).toHaveLength(1);
  fireEvent.click(
    screen.getByRole("button", { name: "Проверить результат добавления" }),
  );
  await screen.findByText(/Статус добавления проверен/);
});
it("shows conflicting registration without transferring ownership", async () => {
  const fetcher = setup("OWNERSHIP_CONFLICT");
  const card = await moyCard();
  settings(card);
  await verify(card);
  fireEvent.click(within(card).getByLabelText(/Подтверждаю, что этот аккаунт/));
  fireEvent.click(
    within(card).getByRole("button", { name: "Добавить аккаунт" }),
  );
  await screen.findByText(/Перенос недоступен/);
  expect(
    fetcher.mock.calls.filter(([p]) => String(p).endsWith("/register")),
  ).toHaveLength(1);
});
type ProviderFixture = {
  provider: string;
  account: string;
  status: string;
  registration: string;
  import?: typeof batch;
  check?: { checked_at: string; identity: string };
};
function mockRegistered(
  providers: ProviderFixture[] = [
    {
      provider: "moysklad",
      account: "moy-account",
      status: "CONFIGURED_NOT_VERIFIED",
      registration: "REGISTERED",
      import: batch,
    },
    {
      provider: "ozon",
      account: "ozon-account",
      status: "CONFIGURED_NOT_VERIFIED",
      registration: "REGISTERED",
      import: batch,
    },
  ],
) {
  return vi
    .spyOn(globalThis, "fetch")
    .mockImplementation(
      async (input) =>
        new Response(
          JSON.stringify(
            String(input).startsWith("/api/imports?")
              ? { items: [], has_next: false, offset: 0 }
              : { providers },
          ),
          { status: 200 },
        ),
    );
}
it("offers mapping review only when both selected registered accounts have eligible imports", async () => {
  const fetcher = mockRegistered();
  mount({ accounts, selected, report });
  const next = await screen.findByRole("region", { name: "Следующий шаг" });
  expect(
    within(next).getByRole("heading", {
      name: "Данные обеих систем загружены",
    }),
  ).toBeTruthy();
  expect(
    within(next)
      .getByRole("link", { name: "Сопоставить товары" })
      .getAttribute("href"),
  ).toBe("/products");
  expect(
    within(next)
      .getByRole("link", { name: "Сопоставить склады" })
      .getAttribute("href"),
  ).toBe("/warehouses");
  expect(within(next).getByText(/Публикация остатков выключена/)).toBeTruthy();
  expect(fetcher.mock.calls.some(([, init]) => init?.method === "POST")).toBe(
    false,
  );
  const card = await moyCard();
  expect(within(card).getByText("Аккаунт добавлен")).toBeTruthy();
  expect(within(card).getByText(/доступ не проверен/)).toBeTruthy();
  expect(within(card).queryByText(/^Доступ проверен/)).toBeNull();
});
it("does not use configured-account imports to claim selected-account readiness", async () => {
  mockRegistered();
  mount({
    accounts,
    selected,
    report: {
      ...report,
      batches: { moysklad: batch, ozon: { ...batch, completed: false } },
    },
  });
  const next = await screen.findByRole("region", { name: "Следующий шаг" });
  expect(
    within(next).getByRole("heading", { name: "Загрузите первый каталог" }),
  ).toBeTruthy();
  expect(
    within(next).queryByRole("link", { name: "Сопоставить товары" }),
  ).toBeNull();
  expect(within(next).queryByText("Данные обеих систем загружены")).toBeNull();
});
it("keeps identifiers behind technical disclosure and account selection in settings", async () => {
  const writeText = vi.fn().mockResolvedValue(undefined);
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: { writeText },
  });
  mockRegistered();
  const onSelect = vi.fn();
  mount({
    accounts: [...accounts, { ...accounts[0], account: "second-moy" }],
    selected,
    report,
    onSelect,
  });
  const card = await moyCard();
  expect(within(card).queryByRole("combobox")).toBeNull();
  expect(within(card).queryByRole("dialog")).toBeNull();
  expect(
    within(card).queryByRole("button", { name: "Копировать идентификаторы" }),
  ).toBeNull();
  expect(within(card).getByText("Аккаунт 1 из 2")).toBeTruthy();
  settings(card);
  fireEvent.change(
    within(card).getByRole("combobox", { name: "МойСклад: аккаунт" }),
    { target: { value: "second-moy" } },
  );
  expect(onSelect).toHaveBeenCalledWith("moysklad", "second-moy");
  const technical = within(card)
    .getByText("Технические детали")
    .closest("details")!;
  expect(technical.open).toBe(false);
  fireEvent.click(within(technical).getByText("Технические детали"));
  expect(technical.open).toBe(true);
  expect(within(technical).getByText(tenant)).toBeTruthy();
  const accountList = technical.querySelector<HTMLElement>(
    ".technical-account-list",
  )!;
  expect(within(accountList).getByText("moy-account")).toBeTruthy();
  expect(within(accountList).getByText("second-moy")).toBeTruthy();
  expect(within(accountList).getByText(/^Аккаунт 2/)).toBeTruthy();
  expect(
    within(technical).getByRole("button", {
      name: "Копировать идентификаторы",
    }),
  ).toBeTruthy();
  fireEvent.click(
    within(technical).getByRole("button", {
      name: "Копировать идентификаторы",
    }),
  );
  await within(technical).findByRole("button", { name: "Скопировано" });
  expect(writeText).toHaveBeenCalledWith(
    expect.stringContaining("account 2: second-moy"),
  );
});
it("blocks registration when the successful probe is stale or has a future timestamp", async () => {
  for (const checked_at of [
    new Date(Date.now() - 6 * 60 * 1000).toISOString(),
    new Date(Date.now() + 60 * 1000).toISOString(),
  ]) {
    mockRegistered([
      {
        provider: "moysklad",
        account: "moy-account",
        status: "VERIFIED",
        registration: "NOT_REGISTERED",
        import: batch,
        check: { checked_at, identity: "NOT_ESTABLISHED" },
      },
    ]);
    mount();
    const card = await moyCard();
    settings(card);
    fireEvent.click(
      within(card).getByLabelText(/Подтверждаю, что этот аккаунт/),
    );
    expect(
      (
        within(card).getByRole("button", {
          name: "Добавить аккаунт",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
    cleanup();
    vi.restoreAllMocks();
  }
});
it("explains missing configuration through the contextual next step", async () => {
  setup();
  await moyCard();
  const next = screen.getByRole("region", { name: "Следующий шаг" });
  expect(
    within(next).getByRole("heading", { name: "Добавьте аккаунт МойСклад" }),
  ).toBeTruthy();
  fireEvent.click(
    within(next).getByRole("button", { name: "Настроить МойСклад" }),
  );
  await waitFor(() =>
    expect(screen.getAllByText("Настройки")[0].closest("details")?.open).toBe(
      true,
    ),
  );
  expect(screen.queryByRole("link", { name: "Сопоставить товары" })).toBeNull();
});

for (const [state, heading] of [
  ["RUNNING", "Дождитесь завершения загрузки"],
  ["OUTCOME_UNKNOWN", "Дождитесь уточнения результата"],
] as const)
  it(`shows a contextual wait instead of another setup or load action for ${state}`, async () => {
    const operation = {
      id: "a".repeat(32),
      tenant,
      account: selected.ozon,
      provider: "ozon",
      operation_type: "OZON_CATALOG_IMPORT",
      state,
      phase: state === "RUNNING" ? "READING_PRODUCTS" : "PERSISTING",
      outcome: null,
      limiting_resource: null,
      created_at: "2026-01-01T00:00:00Z",
      started_at: "2026-01-01T00:00:01Z",
      finished_at: null,
      attempt_started_at: "2026-01-01T00:00:01Z",
      progress_at: null,
      lease_expires_at: null,
      attempt: 1,
      max_attempts: 3,
      elapsed_ms: 1000,
      batch_id: null,
      batch_created: null,
      counts: {
        requests: 1,
        attempts: 1,
        pages: 1,
        completed_requests: 1,
        completed_pages: 1,
        products: 8,
        warehouses: 0,
        observations: 0,
      },
      limits: {
        requests: 100,
        attempts: 100,
        pages: 100,
        products: 1000,
        warehouses: 1000,
        observations: 1000,
        response_bytes: 4194304,
        duration_ns: 180000000000,
        database_timeout_ns: 10000000000,
      },
    };
    const fetcher = mockRegistered();
    const defaultFetch = fetcher.getMockImplementation()!;
    fetcher.mockImplementation(async (input, init) => {
      const path = String(input);
      if (path.startsWith("/api/imports?")) {
        const query = new URLSearchParams(path.split("?")[1]);
        return new Response(
          JSON.stringify({
            items: query.has("ozon_account") ? [operation] : [],
            has_next: false,
            offset: 0,
          }),
          { status: 200 },
        );
      }
      if (path.startsWith("/api/imports/"))
        return new Response(JSON.stringify(operation), { status: 200 });
      return defaultFetch(input, init);
    });
    mount({
      accounts,
      selected,
      report: {
        ...report,
        batches: {
          moysklad: batch,
          ozon: { ...batch, present: false, completed: false },
        },
      },
    });
    const next = await screen.findByRole("region", { name: "Следующий шаг" });
    await within(next).findByRole("heading", { name: heading });
    expect(within(next).queryByRole("button")).toBeNull();
    expect(within(next).queryByRole("link")).toBeNull();
    expect(within(next).queryByText("Загрузите первый каталог")).toBeNull();
    const ozon = screen.getByRole("article", { name: "Ozon" });
    expect(
      (
        within(ozon).getByRole("button", {
          name: "Загрузить данные",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
    expect(fetcher.mock.calls.some(([, init]) => init?.method === "POST")).toBe(
      false,
    );
  });

it("takes a configured first-import next step to the selected provider action without submitting", async () => {
  const fetcher = mockRegistered();
  mount({
    accounts,
    selected,
    report: {
      ...report,
      batches: {
        moysklad: batch,
        ozon: { ...batch, present: false, completed: false },
      },
    },
  });
  const next = await screen.findByRole("region", { name: "Следующий шаг" });
  const ozon = screen.getByRole("article", { name: "Ozon" });
  const action = within(ozon).getByRole("button", {
    name: "Загрузить данные",
  }) as HTMLButtonElement;
  await waitFor(() => expect(action.disabled).toBe(false));
  fireEvent.click(
    within(next).getByRole("button", { name: "К загрузке данных" }),
  );
  expect(document.activeElement).toBe(action);
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(fetcher.mock.calls.some(([, init]) => init?.method === "POST")).toBe(
    false,
  );
});
