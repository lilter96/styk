import "@testing-library/jest-dom/vitest";
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
import { OrdersOperations } from "./OrdersOperations";
import {
  historyActionTitle,
  historyDay,
  historyGroups,
  historyResult,
  historyRows,
  historyTime,
} from "./OrdersHistoryPresentation";
import {
  orderScopeBody,
  type OrderOperation,
  type OrderScope,
  type OrderWork,
} from "./OrdersTypes";

const scope: OrderScope = { tenant: "history-test", moy: "moy", ozon: "ozon" };
const native = orderScopeBody(scope);
function work(
  id = "one",
  state = "RECONCILED",
  posting = "POSTING-1",
): OrderWork {
  return {
    id,
    scope: native,
    state,
    automatic: true,
    command: {
      id: `command-${id}`,
      scope: native,
      action: "mirror_update",
      posting_number: posting,
      commercial_order_id: "ORDER-1",
      correlation: {
        customer_order_id: "11111111-1111-4111-8111-111111111111",
      },
    },
    receipt: { state },
  };
}
function operation(
  targets: OrderWork[] = [work()],
  id = "operation-1",
): OrderOperation {
  return {
    id,
    scope: native,
    state: "UNKNOWN",
    created_at: "2026-11-05T10:32:00Z",
    targets,
  };
}
const noFilters = { search: "", status: "", attention: false };
const response = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
function mount(
  handler: (url: URL, init?: RequestInit) => Response | Promise<Response>,
) {
  const fetcher = vi.fn(async (path: RequestInfo | URL, init?: RequestInit) =>
    handler(new URL(String(path), "http://localhost"), init),
  );
  vi.stubGlobal("fetch", fetcher);
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  const onOutcomes = vi.fn();
  const view = render(
    <QueryClientProvider client={client}>
      <div className="orders-console">
        <OrdersOperations scope={scope} onOutcomes={onOutcomes} />
      </div>
    </QueryClientProvider>,
  );
  return {
    fetcher,
    onOutcomes,
    client,
    ...view,
    changeScope: (next: OrderScope) =>
      view.rerender(
        <QueryClientProvider client={client}>
          <div className="orders-console">
            <OrdersOperations scope={next} onOutcomes={onOutcomes} />
          </div>
        </QueryClientProvider>,
      ),
  };
}
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

it.each([
  ["QUEUED", "В обработке", "progress"],
  ["CLAIMED", "Проверяем условия", "progress"],
  ["DISPATCHING", "Отправляем", "progress"],
  ["ACKNOWLEDGED", "Ждём подтверждения", "progress"],
  ["POLLING", "Проверяем результат", "progress"],
  ["RECONCILED", "Готово", "success"],
  ["UNKNOWN", "Нужна проверка", "warning"],
  ["REJECTED", "Отклонено", "danger"],
  ["BLOCKED", "Действие заблокировано", "warning"],
  ["FUTURE", "Нет подтверждённых сведений", "neutral"],
])("presents %s without implying unconfirmed success", (state, label, tone) => {
  expect(historyResult(state)).toMatchObject({ label, tone });
  expect(historyResult(state).tone === "success").toBe(state === "RECONCILED");
});
it("uses work state, stable compound identities, all targets and a truthful empty-operation fallback", () => {
  const targets = [work(), work("two", "UNKNOWN", "POSTING-2")];
  const rows = historyRows(
    [
      operation([...targets, targets[0]]),
      operation([targets[0]], "other"),
      operation([], "empty"),
    ],
    noFilters,
  );
  expect(rows).toHaveLength(4);
  expect(new Set(rows.map((row) => row.id)).size).toBe(4);
  expect(rows[0].work?.state).toBe("RECONCILED");
  expect(historyResult(rows[3].work?.state).tone).toBe("neutral");
  expect(
    historyRows([operation(targets)], {
      search: "POSTING-1",
      status: "UNKNOWN",
      attention: true,
    }),
  ).toEqual([]);
  expect(
    historyRows([operation(targets)], { ...noFilters, attention: true }).map(
      (row) => row.work?.id,
    ),
  ).toEqual(["two"]);
});
it("groups real local dates in server order and tolerates missing dates and actions", () => {
  const now = new Date(2026, 10, 5, 15);
  const today = new Date(2026, 10, 5, 12).toISOString();
  const yesterday = new Date(2026, 10, 4, 22).toISOString();
  expect(historyDay(today, now)).toContain("Сегодня, 5 ноября");
  expect(historyDay(yesterday, now)).toContain("Вчера, 4 ноября");
  expect(historyTime("0001-01-01T00:00:00Z")).toBe("—");
  expect(historyDay("invalid", now)).toBe("Дата не указана");
  expect(historyActionTitle("future_action")).toBe("Действие с заказом");
  const rows = historyRows(
    [
      { ...operation(), created_at: today },
      { ...operation([], "old"), created_at: yesterday },
    ],
    noFilters,
  );
  expect(
    historyGroups(rows).map((group) => group.rows[0].operation.id),
  ).toEqual(["operation-1", "old"]);
});
it("opens only the chosen work and defers technical content without making a write", async () => {
  const { fetcher } = mount(() =>
    response({
      operations: [
        operation([work(), work("two", "ACKNOWLEDGED", "POSTING-2")]),
      ],
    }),
  );
  const opener = await screen.findByRole("button", {
    name: /Подробнее: POSTING-2/,
  });
  opener.focus();
  fireEvent.click(opener);
  const dialog = screen.getByRole("dialog");
  expect(
    within(dialog).getByRole("heading", { name: "Ждём подтверждения" }),
  ).toBeVisible();
  expect(within(dialog).queryByText("Готово")).toBeNull();
  expect(within(dialog).queryByText("Work ID")).toBeNull();
  const details = within(dialog)
    .getByText("Технические сведения")
    .closest("details")!;
  details.open = true;
  fireEvent(details, new Event("toggle"));
  await within(dialog).findByText("Work ID");
  expect(within(dialog).getByText("two", { exact: true })).toBeVisible();
  expect(
    within(dialog).getByRole("link", { name: "Открыть заказ МойСклад" }),
  ).toHaveAttribute("rel", "noopener noreferrer");
  fireEvent.click(
    within(dialog).getByRole("button", {
      name: "Обновить статус",
    }),
  );
  await waitFor(() => expect(fetcher.mock.calls.length).toBe(2));
  expect(
    fetcher.mock.calls.every(
      ([, init]) => !init?.method || init.method === "GET",
    ),
  ).toBe(true);
  fireEvent.click(within(dialog).getByRole("button", { name: "Закрыть" }));
  expect(opener).toHaveFocus();
  expect(document.body.style.overflow).toBe("");
});
it("keeps unknown recovery and important failures outside technical details", async () => {
  const unknown = {
    ...work("unknown", "UNKNOWN"),
    code: "NATIVE_DOCUMENT_DELETED_REQUIRES_REVIEW",
  };
  const { fetcher } = mount((url) =>
    response(
      url.pathname.endsWith("/recovery")
        ? {
            work: unknown,
            eligible: false,
            blockers: [{ code: "UNKNOWN_ACTION_DISCOVERY_ONLY" }],
          }
        : { operations: [operation([unknown])] },
    ),
  );
  fireEvent.click(
    await screen.findByRole("button", { name: /Разобраться: POSTING-1/ }),
  );
  const dialog = screen.getByRole("dialog");
  expect(within(dialog).getByText(/Запрос мог выполниться/)).toBeVisible();
  expect(within(dialog).getByText(/может создать дубликат/)).toBeVisible();
  expect(within(dialog).getByText(/Складской документ удалён/)).toBeVisible();
  const recovery = within(dialog)
    .getByText("Проверить неизвестный результат и варианты восстановления")
    .closest("details")!;
  recovery.open = true;
  fireEvent(recovery, new Event("toggle"));
  await within(dialog).findByText(
    /Для этого действия доступна только проверка/,
  );
  expect(within(dialog).queryByText("Work ID")).toBeNull();
  expect(
    fetcher.mock.calls.every(
      ([, init]) => !init?.method || init.method === "GET",
    ),
  ).toBe(true);
});
it("filters on the server, resets cursors and keeps latest safety outcomes unfiltered", async () => {
  const pages: URLSearchParams[] = [];
  const { onOutcomes } = mount((url) => {
    pages.push(url.searchParams);
    const attention = url.searchParams.get("attention") === "true";
    const older = !!url.searchParams.get("cursor");
    return response({
      operations: attention
        ? [
            operation([
              work("unknown", "UNKNOWN", older ? "OLDER" : "POSTING-2"),
            ]),
          ]
        : [operation([work(), work("unknown", "UNKNOWN", "POSTING-2")])],
      next_cursor: older ? undefined : "cursor-2",
    });
  });
  await screen.findByRole("button", { name: /Подробнее: POSTING-1/ });
  fireEvent.click(screen.getByRole("button", { name: "Нужна проверка" }));
  await waitFor(() =>
    expect(
      screen.queryByRole("button", { name: /Подробнее: POSTING-1/ }),
    ).toBeNull(),
  );
  await screen.findByRole("button", { name: /Разобраться: POSTING-2/ });
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Следующие" })).toBeEnabled(),
  );
  expect(onOutcomes).toHaveBeenLastCalledWith({
    "POSTING-1": "RECONCILED",
    "POSTING-2": "UNKNOWN",
  });
  fireEvent.click(screen.getByRole("button", { name: "Следующие" }));
  await screen.findByRole("button", { name: /Разобраться: OLDER/ });
  fireEvent.change(screen.getByLabelText("Результат операции"), {
    target: { value: "REJECTED" },
  });
  await waitFor(() =>
    expect(
      pages.some(
        (params) =>
          params.get("status") === "REJECTED" && params.get("cursor") === "",
      ),
    ).toBe(true),
  );
  expect(screen.getByText("Страница 1")).toBeVisible();
  fireEvent.change(screen.getByLabelText("Номер отправления в истории"), {
    target: { value: "EXACT-42" },
  });
  await waitFor(() =>
    expect(pages.some((params) => params.get("search") === "EXACT-42")).toBe(
      true,
    ),
  );
  await screen.findByText("Операции не найдены");
});
it("isolates accounts, closes details and rejects mismatched responses", async () => {
  const next = { ...scope, ozon: "second" };
  const view = mount((url) =>
    response({
      operations:
        url.searchParams.get("ozon_account") === scope.ozon
          ? [operation()]
          : [operation()],
    }),
  );
  fireEvent.click(
    await screen.findByRole("button", { name: /Подробнее: POSTING-1/ }),
  );
  expect(screen.getByRole("dialog")).toBeVisible();
  view.changeScope(next);
  expect(screen.queryByRole("dialog")).toBeNull();
  await screen.findByRole("alert");
  expect(
    screen.queryByRole("button", { name: /Подробнее: POSTING-1/ }),
  ).toBeNull();
  expect(screen.getByLabelText("Номер отправления в истории")).toHaveValue("");
  expect(screen.getByText("Страница 1")).toBeVisible();
});
it("renders empty and failed history with working reset and retry", async () => {
  let fail = true;
  const { fetcher } = mount(() =>
    response(
      fail ? { error: "UNAVAILABLE" } : { operations: [] },
      fail ? 503 : 200,
    ),
  );
  await screen.findByRole("alert");
  fail = false;
  fireEvent.click(
    screen.getByRole("button", { name: "Повторить загрузку истории" }),
  );
  await screen.findByText("Действий пока нет");
  fireEvent.change(screen.getByLabelText("Результат операции"), {
    target: { value: "UNKNOWN" },
  });
  await screen.findByText("Операции не найдены");
  fireEvent.click(
    screen.getByRole("button", { name: "Сбросить фильтры истории" }),
  );
  await screen.findByText("Действий пока нет");
  expect(fetcher.mock.calls.length).toBeGreaterThanOrEqual(3);
});
it("withholds unconfirmed, expired and unsafe artifacts and invalid native links", async () => {
  const targets = [
    work("pending", "ACKNOWLEDGED"),
    work("expired"),
    work("unsafe"),
  ].map((item) => ({
    ...item,
    command: {
      ...item.command,
      action: "posting_label_create",
      correlation: { customer_order_id: "not-a-valid-id" },
    },
    receipt: {
      state: item.state,
      artifact: {
        available: true,
        download_url:
          item.id === "unsafe"
            ? "https://evil.test/file"
            : "/api/orders/artifacts/label",
        expires_at:
          item.id === "expired"
            ? "2000-01-01T00:00:00Z"
            : "2099-01-01T00:00:00Z",
      },
    },
  }));
  mount(() =>
    response({
      operations: targets.map((target) => operation([target], target.id)),
    }),
  );
  const openers = await screen.findAllByRole("button", {
    name: /Подробнее: POSTING-1/,
  });
  for (const [index, opener] of openers.entries()) {
    fireEvent.click(opener);
    const dialog = screen.getByRole("dialog");
    expect(within(dialog).queryByRole("link")).toBeNull();
    if (index === 1)
      expect(
        within(dialog).getByText("Срок хранения этикетки истёк."),
      ).toBeVisible();
    fireEvent.click(within(dialog).getByRole("button", { name: "Закрыть" }));
  }
});

it("closes a resolved filtered row and does not reopen it on a later refresh", async () => {
  let state = "UNKNOWN";
  mount((url) =>
    response({
      operations:
        url.searchParams.get("attention") === "true" && state !== "UNKNOWN"
          ? []
          : [operation([work("one", state)])],
    }),
  );
  await screen.findByRole("button", { name: /Разобраться: POSTING-1/ });
  fireEvent.click(screen.getByRole("button", { name: "Нужна проверка" }));
  const opener = await screen.findByRole("button", {
    name: /Разобраться: POSTING-1/,
  });
  await waitFor(() =>
    expect(
      screen.getByRole("button", { name: "Обновить статусы" }),
    ).toBeEnabled(),
  );
  fireEvent.click(opener);
  state = "RECONCILED";
  fireEvent.click(
    within(screen.getByRole("dialog")).getByRole("button", {
      name: "Обновить статус",
    }),
  );
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  state = "UNKNOWN";
  fireEvent.click(screen.getByRole("button", { name: "Обновить статусы" }));
  await screen.findByRole("button", { name: /Разобраться: POSTING-1/ });
  expect(screen.queryByRole("dialog")).toBeNull();
});
it("shows refresh failures inside the drawer alongside the last known result", async () => {
  let fail = false;
  mount(() =>
    response(
      fail ? { error: "UNAVAILABLE" } : { operations: [operation()] },
      fail ? 503 : 200,
    ),
  );
  fireEvent.click(
    await screen.findByRole("button", { name: /Подробнее: POSTING-1/ }),
  );
  const dialog = screen.getByRole("dialog");
  fail = true;
  fireEvent.click(
    within(dialog).getByRole("button", { name: "Обновить статус" }),
  );
  expect(await within(dialog).findByRole("alert")).toHaveTextContent(
    "Показано последнее полученное состояние",
  );
  expect(within(dialog).getByRole("heading", { name: "Готово" })).toBeVisible();
});
