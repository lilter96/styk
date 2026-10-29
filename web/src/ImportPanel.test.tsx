import { useState } from "react";
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
import { MemoryRouter } from "react-router-dom";
import type { Batch } from "./api";
import { ImportPanel } from "./ImportPanel";
import {
  ImportSubmissionNotice,
  useImportSubmission,
} from "./ImportSubmission";
import type { ImportOperation, ImportScope, ImportProvider } from "./api";
const scope: ImportScope = {
  provider: "ozon",
  tenant: "tenant-a",
  account: "ozon-a",
};
const base: ImportOperation = {
  id: "a".repeat(32),
  tenant: scope.tenant,
  account: scope.account,
  provider: "ozon",
  operation_type: "OZON_CATALOG_IMPORT",
  state: "QUEUED",
  phase: "QUEUED",
  outcome: null,
  limiting_resource: null,
  created_at: "2026-01-01T00:00:00Z",
  started_at: null,
  finished_at: null,
  attempt_started_at: null,
  progress_at: null,
  lease_expires_at: null,
  attempt: 0,
  max_attempts: 3,
  elapsed_ms: 0,
  batch_id: null,
  batch_created: null,
  counts: {
    requests: 0,
    attempts: 0,
    pages: 0,
    completed_requests: 0,
    completed_pages: 0,
    products: 0,
    warehouses: 0,
    observations: 0,
  },
  limits: {
    requests: 100,
    attempts: 100,
    pages: 100,
    products: 1000,
    warehouses: 1000,
    observations: 10000,
    response_bytes: 4194304,
    duration_ns: 180000000000,
    database_timeout_ns: 10000000000,
  },
};
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});
function Harness({
  client,
  initialScope = scope,
  configured = true,
  latestBatch,
}: {
  client: QueryClient;
  initialScope?: ImportScope;
  configured?: boolean;
  latestBatch?: Batch;
}) {
  const [current, setCurrent] = useState<ImportScope>(initialScope);
  const submission = useImportSubmission();
  return (
    <>
      <button
        onClick={() =>
          setCurrent({
            provider: current.provider,
            tenant: "tenant-b",
            account: "ozon-b",
          })
        }
      >
        Switch scope
      </button>
      <button
        onClick={() =>
          setCurrent({
            ...current,
            provider: current.provider === "ozon" ? "moysklad" : "ozon",
          })
        }
      >
        Switch provider
      </button>
      <ImportSubmissionNotice
        submission={submission}
        scope={current}
        restore={setCurrent}
      />
      <ImportPanel
        key={`${current.tenant}:${current.provider}:${current.account}`}
        scope={current}
        submission={submission}
        configured={configured}
        latestBatch={latestBatch}
      />
      <button
        onClick={() =>
          void client.invalidateQueries({ queryKey: ["import-history"] })
        }
      >
        Refresh test data
      </button>
    </>
  );
}
function setup(
  opts: {
    provider?: ImportProvider;
    latestBatch?: Batch;
    configured?: boolean;
    initial?: ImportOperation;
    lost?: boolean;
    unauthorized?: boolean;
    conflict?: boolean;
    cancelConflict?: boolean;
    pages?: boolean;
  } = {},
) {
  const fixtureScope: ImportScope = {
    ...scope,
    provider: opts.provider ?? "ozon",
  };
  const fixtureBase: ImportOperation = {
    ...base,
    provider: fixtureScope.provider,
    operation_type:
      fixtureScope.provider === "moysklad"
        ? "MOYSKLAD_CATALOG_IMPORT"
        : "OZON_CATALOG_IMPORT",
  };
  let operation = opts.initial,
    attempts = 0,
    historyUnavailable = false;
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const invalidate = vi.spyOn(client, "invalidateQueries");
  const fetcher = vi
    .spyOn(globalThis, "fetch")
    .mockImplementation(async (input, init) => {
      const path = String(input);
      if (init?.method === "POST" && path === "/api/imports") {
        attempts++;
        if (opts.unauthorized)
          return new Response('{"error":"UNAUTHORIZED"}', { status: 401 });
        if (opts.conflict)
          return new Response('{"error":"IDEMPOTENCY_CONFLICT"}', {
            status: 409,
          });
        operation = { ...fixtureBase };
        if (opts.lost && attempts === 1) throw new TypeError("connection lost");
        return new Response(
          JSON.stringify({ operation, idempotent_replay: !!opts.lost }),
          { status: attempts === 1 ? 201 : 200 },
        );
      }
      if (init?.method === "POST" && path.endsWith("/cancel")) {
        if (opts.cancelConflict) {
          operation = {
            ...fixtureBase,
            state: "RUNNING",
            phase: "READING_PRODUCTS",
          };
          return new Response('{"error":"CANCELLATION_UNAVAILABLE"}', {
            status: 409,
          });
        }
        operation = {
          ...fixtureBase,
          state: "CANCELLED",
          phase: "CANCELLED",
          outcome: "CANCELLED",
        };
        return new Response(null, { status: 204 });
      }
      if (path.startsWith("/api/imports?")) {
        if (historyUnavailable)
          return new Response('{"error":"IMPORT_PERSISTENCE_UNKNOWN"}', {
            status: 503,
          });
        const query = new URLSearchParams(path.split("?")[1]);
        if (
          query.get("tenant") !== fixtureScope.tenant ||
          (query.has("moysklad_account") ? "moysklad" : "ozon") !==
            fixtureScope.provider
        )
          return new Response('{"items":[],"has_next":false,"offset":0}', {
            status: 200,
          });
        if (opts.pages && query.get("offset") === "10")
          return new Response(
            JSON.stringify({
              items: [
                {
                  ...base,
                  id: "b".repeat(32),
                  state: "FAILED",
                  outcome: "API_FAILURE",
                },
              ],
              has_next: false,
              offset: 10,
            }),
            { status: 200 },
          );
        return new Response(
          JSON.stringify({
            items: operation ? [operation] : [],
            has_next: !!opts.pages,
            offset: 0,
          }),
          { status: 200 },
        );
      }
      if (path.startsWith("/api/imports/"))
        return new Response(JSON.stringify(operation || base), { status: 200 });
      throw new Error("unexpected request");
    });
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <Harness
          client={client}
          initialScope={fixtureScope}
          configured={opts.configured ?? true}
          latestBatch={opts.latestBatch}
        />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return {
    fetcher,
    client,
    invalidate,
    setOperation: (value: ImportOperation) => {
      operation = value;
    },
    setHistoryUnavailable: (value: boolean) => {
      historyUnavailable = value;
    },
  };
}
async function primary(name = "Ozon") {
  const panel = await screen.findByRole("region", {
    name: `${name}: импорт данных`,
  });
  return within(panel).getByRole("button", {
    name: /^(Загрузить|Обновить) данные$/,
  }) as HTMLButtonElement;
}
async function openConfirm(name = "Ozon") {
  const button = await primary(name);
  await waitFor(() => expect(button.disabled).toBe(false));
  fireEvent.click(button);
  return screen.findByRole("dialog", { name: `Загрузить данные ${name}?` });
}
async function submit(name = "Ozon") {
  const dialog = await openConfirm(name);
  fireEvent.click(within(dialog).getByRole("checkbox"));
  fireEvent.click(
    within(dialog).getByRole("button", { name: "Подтвердить загрузку" }),
  );
}
async function openHistory(container: HTMLElement = document.body, index = 0) {
  let history: HTMLDetailsElement | null = null;
  await waitFor(() => {
    history = container.querySelector<HTMLDetailsElement>(".provider-history");
    expect(history).toBeTruthy();
    expect(history!.querySelectorAll(".history-entry").length).toBeGreaterThan(
      index,
    );
  });
  if (!history!.open) fireEvent.click(history!.querySelector("summary")!);
  const row =
    history!.querySelectorAll<HTMLDetailsElement>(".history-entry")[index];
  if (!row.open) fireEvent.click(row.querySelector("summary")!);
  fireEvent(row, new Event("toggle"));
  return row;
}
function openTechnical(row: HTMLElement) {
  const details = row.querySelector<HTMLDetailsElement>(".technical-details")!;
  if (!details.open) fireEvent.click(details.querySelector("summary")!);
  return details;
}
async function cancel() {
  const progress = document.querySelector<HTMLElement>(".import-progress")!;
  fireEvent.click(
    within(progress).getByRole("button", { name: "Отменить загрузку" }),
  );
  const dialog = await screen.findByRole("dialog", {
    name: "Отменить загрузку?",
  });
  fireEvent.click(
    within(dialog).getByRole("button", { name: "Подтвердить отмену" }),
  );
}
it("requires acknowledgement and safely retries one immutable intent after switching accounts", async () => {
  const { fetcher } = setup({ lost: true });
  await screen.findByText("Загрузок пока нет");
  expect(
    fetcher.mock.calls.filter(([, init]) => init?.method === "POST"),
  ).toHaveLength(0);
  const dialog = await openConfirm();
  const button = within(dialog).getByRole("button", {
    name: "Подтвердить загрузку",
  }) as HTMLButtonElement;
  expect(button.disabled).toBe(true);
  fireEvent.click(within(dialog).getByRole("checkbox"));
  fireEvent.click(button);
  fireEvent.click(button);
  await screen.findByText(/Результат отправки неизвестен/);
  fireEvent.click(screen.getByText("Switch scope"));
  expect(
    screen.queryByRole("button", { name: "Повторить тот же запрос" }),
  ).toBeNull();
  fireEvent.click(screen.getByText("Вернуться к исходному аккаунту"));
  fireEvent.click(
    screen.getByRole("button", { name: "Повторить тот же запрос" }),
  );
  await screen.findByText(/Ранее созданная загрузка найдена/);
  const calls = fetcher.mock.calls.filter(
    ([p, init]) => p === "/api/imports" && init?.method === "POST",
  );
  expect(calls).toHaveLength(2);
  expect(calls[0][1]?.body).toBe(calls[1][1]?.body);
  const payload = JSON.parse(String(calls[0][1]?.body));
  expect(payload.tenant).toBe(scope.tenant);
  expect(payload.ozon_account).toBe(scope.account);
  expect(payload.idempotency_key).toMatch(/^[0-9a-f-]{36}$/);
  expect(payload.limits).toBeUndefined();
});
it("reports when secure request identity cannot be generated", async () => {
  vi.spyOn(crypto, "randomUUID").mockImplementation(() => {
    throw new Error("unavailable");
  });
  const { fetcher } = setup();
  await submit();
  await screen.findByText(/Браузер не может создать защищённый запрос/);
  expect(
    fetcher.mock.calls.filter(([, init]) => init?.method === "POST"),
  ).toHaveLength(0);
});
it("renders actual attempt-local progress and refreshes source queries after success", async () => {
  const running: ImportOperation = {
    ...base,
    state: "RUNNING",
    phase: "READING_STOCK",
    attempt: 1,
    counts: { ...base.counts, products: 8, completed_pages: 3 },
  };
  const { client, invalidate, setOperation } = setup({ initial: running });
  await screen.findByText(/Обнаружено товаров: 8/);
  expect(screen.getByText("Получаем остатки…")).toBeTruthy();
  expect(document.body.textContent).not.toContain("%");
  const key = [
    "import-detail",
    scope.tenant,
    scope.provider,
    scope.account,
    base.id,
  ];
  const retrying = {
    ...running,
    attempt: 2,
    counts: { ...running.counts, products: 2 },
  };
  setOperation(retrying);
  await client.refetchQueries({ queryKey: key, exact: true });
  await screen.findByText(/Обнаружено товаров: 2/);
  expect(screen.queryByText(/Обнаружено товаров: 8/)).toBeNull();
  const row = await openHistory();
  const technical = openTechnical(row);
  expect(within(technical).getByText("2 из 3")).toBeTruthy();
  const succeeded: ImportOperation = {
    ...running,
    state: "SUCCEEDED",
    phase: "COMPLETED",
    outcome: "COMPLETE_TRAVERSAL",
    batch_id: 42,
    batch_created: true,
  };
  setOperation(succeeded);
  await client.refetchQueries({ queryKey: key, exact: true });
  await waitFor(() =>
    expect(
      invalidate.mock.calls.some(([arg]) =>
        JSON.stringify(arg).includes('"products"'),
      ),
    ).toBe(true),
  );
  for (const expected of ["integrations", "diagnostics", "warehouses"])
    expect(
      invalidate.mock.calls.some(([arg]) =>
        JSON.stringify(arg).includes(`"${expected}"`),
      ),
    ).toBe(true);
});
it("does not offer another import while a newly queued result is newer than cached history", async () => {
  const { client } = setup();
  await submit();
  await screen.findByText("Запрос на загрузку принят.");
  client.setQueryData(
    ["import-history", scope.tenant, scope.provider, scope.account, 0],
    { items: [], has_next: false, offset: 0 },
  );
  await waitFor(async () => expect((await primary()).disabled).toBe(true));
});
for (const [state, outcome, description] of [
  ["FAILED", "LIMIT_REACHED", /Достигнут лимит загрузки/],
  ["CANCELLED", "CANCELLED", /Загрузка отменена до начала обработки/],
  ["OUTCOME_UNKNOWN", null, /Не запускайте повторную загрузку/],
] as const)
  it(`displays ${state} without inventing a result`, async () => {
    setup({
      initial: {
        ...base,
        state,
        outcome,
        limiting_resource: outcome === "LIMIT_REACHED" ? "pages" : null,
      },
    });
    await screen.findAllByText(description);
    expect((await primary()).disabled).toBe(state === "OUTCOME_UNKNOWN");
    expect(screen.queryByText("Последняя успешная загрузка")).toBeNull();
  });
it("cancels only a queued operation after confirmation", async () => {
  const { fetcher } = setup({ initial: base });
  await screen.findByText("В очереди", { selector: ".import-progress strong" });
  await cancel();
  await screen.findByText("Загрузка отменена.");
  const calls = fetcher.mock.calls.filter(([p]) =>
    String(p).endsWith("/cancel"),
  );
  expect(calls).toHaveLength(1);
  expect(JSON.parse(String(calls[0][1]?.body))).toEqual({
    tenant: scope.tenant,
    ozon_account: scope.account,
    acknowledge: true,
  });
});
it("reports a cancellation race and does not claim success", async () => {
  setup({ initial: base, cancelConflict: true });
  await screen.findByText("В очереди", { selector: ".import-progress strong" });
  await cancel();
  await screen.findByText(/Отменить можно только загрузку в очереди/);
  expect(screen.queryByText("Загрузка отменена.")).toBeNull();
  expect(document.querySelector(".import-progress")?.textContent).toContain(
    "Обновляем данные Ozon",
  );
});
it("paginates bounded history without losing the newest scoped operation", async () => {
  const running: ImportOperation = {
    ...base,
    state: "RUNNING",
    phase: "READING_PRODUCTS",
  };
  const { fetcher } = setup({ initial: running, pages: true });
  await screen.findByText("Обновляем данные Ozon");
  const history =
    document.querySelector<HTMLDetailsElement>(".provider-history")!;
  fireEvent.click(history.querySelector("summary")!);
  const next = within(history).getByRole("button", { name: "Следующие" });
  await waitFor(() => expect((next as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(next);
  await within(history).findByText("Ошибка", { selector: ".history-result" });
  const row = await openHistory();
  const technical = openTechnical(row);
  await within(technical).findByText("b".repeat(32));
  expect(screen.getByText("Обновляем данные Ozon")).toBeTruthy();
  expect((await primary()).disabled).toBe(true);
  const urls = fetcher.mock.calls
    .map(([p]) => String(p))
    .filter((p) => p.startsWith("/api/imports?"));
  expect(urls.some((p) => p.includes("offset=10"))).toBe(true);
  expect(
    urls.every(
      (p) =>
        p.includes("limit=10") &&
        p.includes("tenant=tenant-a") &&
        p.includes("ozon_account=ozon-a"),
    ),
  ).toBe(true);
});
it("requires scoped history inspection after session expiration", async () => {
  setup({ unauthorized: true });
  await submit();
  await screen.findByText(/Сессия завершилась/);
  expect(screen.queryByText("Повторить тот же запрос")).toBeNull();
  fireEvent.click(
    screen.getByRole("button", { name: "Обновить и проверить историю" }),
  );
  await screen.findByText("История проверена");
  expect((await primary()).disabled).toBe(true);
  fireEvent.click(screen.getByRole("button", { name: "История проверена" }));
  await waitFor(async () => expect((await primary()).disabled).toBe(false));
});
it("does not allow uncertainty dismissal when refreshing scoped history fails", async () => {
  const { setHistoryUnavailable } = setup({ unauthorized: true });
  await submit();
  await screen.findByText(/Сессия завершилась/);
  setHistoryUnavailable(true);
  fireEvent.click(
    screen.getByRole("button", { name: "Обновить и проверить историю" }),
  );
  await screen.findByText(/История недоступна/, {
    selector: ".import-warning",
  });
  expect(
    screen.getByText("История недоступна", { selector: ".history-preview" }),
  ).toBeTruthy();
  expect(screen.queryByText("Загрузок пока нет")).toBeNull();
  expect(screen.queryByText("История проверена")).toBeNull();
  expect((await primary()).disabled).toBe(true);
});
it("does not create a new intent on idempotency conflict", async () => {
  const { fetcher } = setup({ conflict: true });
  await submit();
  await screen.findByText(/Не удалось однозначно сопоставить запрос/);
  expect(screen.queryByText("Повторить тот же запрос")).toBeNull();
  const code = screen.getByText("IDEMPOTENCY_CONFLICT");
  expect(code.closest("details")?.open).toBe(false);
  expect(
    fetcher.mock.calls.filter(
      ([p, init]) => p === "/api/imports" && init?.method === "POST",
    ),
  ).toHaveLength(1);
});
const moyBase: ImportOperation = {
  ...base,
  provider: "moysklad",
  operation_type: "MOYSKLAD_CATALOG_IMPORT",
  counts: { ...base.counts, variants: 1, warehouse_observations: 2 },
  limits: { ...base.limits, variants: 1000, warehouse_observations: 10000 },
};
it("submits MoySklad only after acknowledgement and preserves its exact intent across providers", async () => {
  const { fetcher, client } = setup({ provider: "moysklad", lost: true });
  await screen.findByText("Загрузок пока нет");
  expect(fetcher.mock.calls.some(([, init]) => init?.method === "POST")).toBe(
    false,
  );
  await submit("МойСклад");
  await screen.findByText(/Результат отправки неизвестен/);
  fireEvent.click(screen.getByText("Switch scope"));
  expect(screen.queryByText("Повторить тот же запрос")).toBeNull();
  fireEvent.click(screen.getByText("Вернуться к исходному аккаунту"));
  fireEvent.click(screen.getByText("Switch provider"));
  await screen.findByRole("region", { name: "Ozon: импорт данных" });
  expect(screen.queryByText("Повторить тот же запрос")).toBeNull();
  fireEvent.click(screen.getByText("Вернуться к исходному аккаунту"));
  fireEvent.click(screen.getByText("Повторить тот же запрос"));
  await screen.findByText(/Ранее созданная загрузка найдена/);
  const calls = fetcher.mock.calls.filter(
    ([url, init]) => url === "/api/imports" && init?.method === "POST",
  );
  expect(calls).toHaveLength(2);
  expect(calls[0][1]?.body).toBe(calls[1][1]?.body);
  const body = JSON.parse(String(calls[0][1]?.body));
  expect(body).toMatchObject({
    tenant: scope.tenant,
    moysklad_account: scope.account,
    operation_type: "MOYSKLAD_CATALOG_IMPORT",
    acknowledge: true,
  });
  expect(body.ozon_account).toBeUndefined();
  expect(body.limits).toBeUndefined();
  expect(body.idempotency_key).toMatch(/^[0-9a-f-]{36}$/);
  for (const provider of ["moysklad", "ozon"])
    expect(
      client
        .getQueryCache()
        .findAll()
        .some(
          (q) =>
            JSON.stringify(q.queryKey) ===
            JSON.stringify([
              "import-history",
              scope.tenant,
              provider,
              scope.account,
              0,
            ]),
        ),
    ).toBe(true);
});
it("keeps MoySklad history readable when credentials are missing", async () => {
  setup({
    provider: "moysklad",
    configured: false,
    initial: {
      ...moyBase,
      state: "FAILED",
      outcome: "CONFIGURATION_UNAVAILABLE",
    },
  });
  await screen.findAllByText(/Настройки выбранного аккаунта недоступны/);
  expect((await primary("МойСклад")).disabled).toBe(true);
  const row = await openHistory();
  expect(row.open).toBe(true);
});
it("renders MoySklad phases and attempt-local counters without inventing progress", async () => {
  const { client, setOperation } = setup({
    provider: "moysklad",
    initial: {
      ...moyBase,
      state: "RUNNING",
      phase: "READING_VARIANTS",
      attempt: 1,
    },
  });
  await screen.findByText("Загружаем варианты…");
  const row = await openHistory();
  const technical = openTechnical(row);
  expect(within(technical).getByText(/вариантов 1/)).toBeTruthy();
  for (const [phase, label] of [
    ["READING_PRODUCTS", "Загружаем товары"],
    ["READING_VARIANTS", "Загружаем варианты"],
    ["READING_WAREHOUSES", "Загружаем склады"],
    ["READING_AGGREGATE_STOCK", "Получаем остатки"],
    ["READING_WAREHOUSE_STOCK", "Получаем остатки по складам"],
    ["PERSISTING", "Сохраняем данные"],
  ]) {
    const operation: ImportOperation = {
      ...moyBase,
      state: "RUNNING",
      phase,
      attempt: 2,
      counts: { ...moyBase.counts, variants: 0 },
    };
    setOperation(operation);
    await client.refetchQueries({
      queryKey: [
        "import-detail",
        scope.tenant,
        "moysklad",
        scope.account,
        base.id,
      ],
      exact: true,
    });
    await screen.findByText(`${label}…`);
    await within(technical).findByText(/вариантов 0/);
  }
  expect(
    within(technical).getByText(/сбрасываются при новой попытке/),
  ).toBeTruthy();
  expect(within(technical).getByText(/записей по складам 2/)).toBeTruthy();
  expect(document.body.textContent).not.toContain("%");
});
for (const state of [
  "QUEUED",
  "FAILED",
  "OUTCOME_UNKNOWN",
  "CANCELLED",
] as const)
  it(`renders MoySklad ${state} without displaying a saved batch`, async () => {
    setup({
      provider: "moysklad",
      initial: {
        ...moyBase,
        state,
        outcome: state === "FAILED" ? "API_FAILURE" : null,
      },
    });
    const row = await openHistory();
    const technical = openTechnical(row);
    expect(within(technical).getAllByText(state).length).toBeGreaterThan(0);
    expect(screen.queryByText("Последняя успешная загрузка")).toBeNull();
    expect(within(technical).queryByText("Пакет данных")).toBeNull();
  });
it("shows a reused MoySklad batch without claiming fresh timestamps and invalidates data", async () => {
  const { invalidate } = setup({
    provider: "moysklad",
    initial: {
      ...moyBase,
      state: "SUCCEEDED",
      phase: "COMPLETED",
      batch_id: 51,
      batch_created: false,
      outcome: "COMPLETE_TRAVERSAL",
    },
  });
  const technical = openTechnical(await openHistory());
  expect(
    within(technical).getByText(/Время наблюдения не изменилось/),
  ).toBeTruthy();
  await waitFor(() =>
    expect(
      invalidate.mock.calls.some(([arg]) =>
        JSON.stringify(arg).includes('"diagnostics"'),
      ),
    ).toBe(true),
  );
  expect(within(technical).getByText(/вариантов 1/)).toBeTruthy();
});
it("cancels a MoySklad queued operation using only its provider scope", async () => {
  const { fetcher } = setup({ provider: "moysklad", initial: moyBase });
  await screen.findByText("В очереди", { selector: ".import-progress strong" });
  await cancel();
  await screen.findByText("Загрузка отменена.");
  const call = fetcher.mock.calls.find(([url]) =>
    String(url).endsWith("/cancel"),
  )!;
  expect(JSON.parse(String(call[1]?.body))).toEqual({
    tenant: scope.tenant,
    moysklad_account: scope.account,
    acknowledge: true,
  });
});
it("handles a MoySklad queued-to-running cancellation race", async () => {
  setup({ provider: "moysklad", initial: moyBase, cancelConflict: true });
  await screen.findByText("В очереди", { selector: ".import-progress strong" });
  await cancel();
  await screen.findByText(/Отменить можно только загрузку в очереди/);
  expect(screen.queryByText("Загрузка отменена.")).toBeNull();
});
it("keeps simultaneous histories isolated when both providers use the same account text", async () => {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
    const url = String(input),
      query = new URLSearchParams(url.split("?")[1]);
    const provider = query.has("moysklad_account") ? "moysklad" : "ozon";
    const op = {
      ...(provider === "moysklad" ? moyBase : base),
      id: provider === "moysklad" ? "m".repeat(32) : base.id,
    };
    return new Response(
      JSON.stringify(
        url.startsWith("/api/imports?")
          ? { items: [op], has_next: false, offset: 0 }
          : op,
      ),
      { status: 200 },
    );
  });
  function Pair() {
    const submission = useImportSubmission();
    return (
      <>
        {(["moysklad", "ozon"] as const).map((provider) => (
          <ImportPanel
            key={provider}
            scope={{ ...scope, provider }}
            configured
            submission={submission}
          />
        ))}
      </>
    );
  }
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <Pair />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  const moy = await screen.findByRole("region", {
    name: "МойСклад: импорт данных",
  });
  const ozon = await screen.findByRole("region", {
    name: "Ozon: импорт данных",
  });
  const moyTechnical = openTechnical(await openHistory(moy));
  const ozonTechnical = openTechnical(await openHistory(ozon));
  expect(within(moyTechnical).getByText("m".repeat(32))).toBeTruthy();
  expect(within(ozonTechnical).getByText(base.id)).toBeTruthy();
  expect(within(moyTechnical).queryByText(base.id)).toBeNull();
  expect(within(ozonTechnical).queryByText("m".repeat(32))).toBeNull();
});
it("keeps the selected MoySklad account's prior persisted import visible after a failed operation", async () => {
  setup({
    provider: "moysklad",
    initial: { ...moyBase, state: "FAILED", outcome: "API_FAILURE" },
    latestBatch: {
      present: true,
      completed: true,
      age: "2h",
      observed_at: "2026-01-01T00:00:00Z",
      counts: { products: 9, variants: 2, warehouses: 3, stock_rows: 0 },
    },
  });
  await screen.findAllByText(/Предыдущие успешные загрузки доступны/);
  const counts = document.querySelector<HTMLElement>(".catalog-counts")!;
  expect(within(counts).getByText("9")).toBeTruthy();
  expect(within(counts).getByText("2")).toBeTruthy();
  expect(within(counts).getByText("3")).toBeTruthy();
  await screen.findByText("Не удалось обновить данные");
  expect((await primary("МойСклад")).textContent).toBe("Обновить данные");
});
it("keeps operation identifiers and diagnostics collapsed until requested and supports copying", async () => {
  const writeText = vi.fn().mockResolvedValue(undefined);
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: { writeText },
  });
  setup({
    initial: {
      ...base,
      state: "SUCCEEDED",
      phase: "COMPLETED",
      batch_id: 42,
      outcome: "COMPLETE_TRAVERSAL",
      elapsed_ms: 1250,
    },
  });
  await screen.findByText("Успешно", { selector: ".history-preview" });
  const history =
    document.querySelector<HTMLDetailsElement>(".provider-history")!;
  const row = history.querySelector<HTMLDetailsElement>(".history-entry")!;
  const technical =
    row.querySelector<HTMLDetailsElement>(".technical-details")!;
  expect(history.open).toBe(false);
  expect(row.open).toBe(false);
  expect(technical.open).toBe(false);
  expect(within(technical).getByText(base.id)).toBeTruthy();
  expect(history.querySelector("summary")?.textContent).not.toContain(base.id);
  expect(row.querySelector("summary")?.textContent).not.toContain(
    "COMPLETE_TRAVERSAL",
  );
  expect(row.querySelector("summary")?.textContent).toContain(
    "1,3 с за попытку",
  );
  await openHistory();
  openTechnical(row);
  fireEvent.click(
    within(technical).getByRole("button", { name: "Скопировать: Операция" }),
  );
  await within(technical).findByText("Скопировано");
  expect(writeText).toHaveBeenCalledWith(base.id);
});

it("uses total worker timestamps for history duration rather than attempt-local elapsed time", async () => {
  setup({
    initial: {
      ...base,
      state: "SUCCEEDED",
      phase: "COMPLETED",
      batch_id: 42,
      outcome: "COMPLETE_TRAVERSAL",
      started_at: "2026-01-01T00:00:01Z",
      finished_at: "2026-01-01T00:00:05Z",
      elapsed_ms: 1250,
    },
  });
  const row = await openHistory();
  expect(row.querySelector(".history-duration")?.textContent).toBe("4 с");
});
