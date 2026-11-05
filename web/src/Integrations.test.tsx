import { afterEach, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { MemoryRouter, useLocation } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { Integrations } from "./Integrations";
import type { Account, Report } from "./api";
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});
const accounts: Account[] = [
  {
    source: "moysklad",
    account: "moy-a",
    name: "Склад А",
    registered_at: "2026-01-01T00:00:00Z",
  },
  {
    source: "ozon",
    account: "ozon-a",
    name: "Основной Ozon",
    registered_at: "2026-01-01T00:00:00Z",
  },
  {
    source: "ozon",
    account: "ozon-b",
    name: "Второй Ozon",
    registered_at: "2026-01-01T00:00:00Z",
  },
];
const batch = {
  present: true,
  completed: true,
  age: "1m",
  observed_at: "2026-01-01T00:00:00Z",
  counts: { products: 14, variants: 4, warehouses: 3 },
};
const report: Report = {
  checks: [],
  batches: { moysklad: batch, ozon: batch },
  publication: "DISABLED",
  mapping_counts: {},
  decision_status: "NOT_CHECKED",
  decision_reasons: [],
};
function Location() {
  const location = useLocation();
  return (
    <output data-testid="location">
      {location.pathname + location.search}
    </output>
  );
}
function fixture(
  options: { errorAccount?: string; importState?: string } = {},
) {
  const views = (moy = "moy-a", ozon = "ozon-a") => [
    {
      provider: "moysklad",
      account: moy,
      status: "VERIFIED",
      registration: "REGISTERED",
      import: batch,
    },
    {
      provider: "ozon",
      account: ozon,
      status: "VERIFIED",
      registration: "REGISTERED",
      import: {
        ...batch,
        counts: { products: ozon === "ozon-b" ? 99 : 22, warehouses: 2 },
      },
    },
  ];
  const operation = {
    id: "a".repeat(32),
    tenant: "merchant",
    provider: "ozon",
    account: "ozon-b",
    state: options.importState,
    operation_type: "OZON_CATALOG_IMPORT",
    phase: "READING_PRODUCTS",
    created_at: "2026-01-01T00:00:00Z",
    started_at: null,
    finished_at: null,
    attempt: 1,
    max_attempts: 3,
    counts: { products: 8 },
    limits: {},
  };
  const requests: { url: string; body: Record<string, unknown> }[] = [];
  const fetcher = vi
    .spyOn(globalThis, "fetch")
    .mockImplementation(async (input, init) => {
      const url = String(input),
        query = new URLSearchParams(url.split("?")[1]);
      const json = (value: unknown, status = 200) =>
        new Response(JSON.stringify(value), { status });
      if (init?.method === "POST") {
        requests.push({ url, body: JSON.parse(String(init.body)) });
        throw new Error("synthetic uncertain response");
      }
      if (url.startsWith("/api/integrations?")) {
        if (
          query.get("ozon_account") === options.errorAccount &&
          options.errorAccount
        )
          return json({ error: "UNAVAILABLE" }, 503);
        return json({
          providers: views(
            query.get("moysklad_account") || "moy-a",
            query.get("ozon_account") || "ozon-a",
          ),
        });
      }
      if (url.includes("/credentials?"))
        return json({
          storage_available: true,
          credential: query.get("account")
            ? {
                tenant: "merchant",
                provider: "ozon",
                account: query.get("account"),
                name:
                  query.get("account") === "ozon-b"
                    ? "Второй Ozon"
                    : "Основной Ozon",
                revision: 3,
                enabled: true,
                checked_at: "2026-01-01T00:00:00Z",
              }
            : null,
        });
      if (url.startsWith("/api/imports?"))
        return json({
          items:
            query.get("ozon_account") === "ozon-b" && options.importState
              ? [operation]
              : [],
          has_next: false,
          offset: 0,
        });
      if (url.startsWith("/api/imports/")) return json(operation);
      if (url.startsWith("/api/stock-sync/eligibility"))
        return json({ state: "PRODUCTION_DISABLED" });
      if (url.startsWith("/api/stock-sync?"))
        return json({
          tenant: "merchant",
          moysklad_account: "moy-a",
          ozon_account: "ozon-a",
          enabled: false,
          version: 0,
          last_operation: null,
        });
      throw new Error("Unexpected synthetic read " + url);
    });
  render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <MemoryRouter>
        <Location />
        <Integrations
          tenant="merchant"
          accounts={accounts}
          selected={{ moy: "moy-a", ozon: "ozon-a" }}
          report={report}
        />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return { requests, fetcher };
}
async function card(name: string) {
  const article = await screen.findByRole("article", { name });
  await within(article).findByText("Работает");
  return article;
}
it("renders actual account groups with logos only in headers and disables unsupported providers", async () => {
  fixture();
  await card("Склад А");
  expect(document.querySelectorAll("[data-provider-group]").length).toBe(4);
  expect(
    document.querySelectorAll(
      "[data-provider-group=ozon] .integration-account-card",
    ).length,
  ).toBe(2);
  expect(
    document.querySelectorAll(".integration-account-card img").length,
  ).toBe(0);
  expect(
    document.querySelectorAll(".integration-provider-header img").length,
  ).toBe(4);
  expect(
    (
      document.querySelector(
        "[data-provider-group=wildberries] .provider-add",
      ) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  expect(
    document.querySelectorAll("[data-provider-group=yandex] article").length,
  ).toBe(0);
});
it("opens source settings using the card account without a modal or mutation", async () => {
  const { requests } = fixture();
  fireEvent.click(
    within(await card("Склад А")).getByRole("button", { name: "Настройки" }),
  );
  expect(screen.getByTestId("location").textContent).toBe(
    "/integrations/moysklad/settings?account=moy-a",
  );
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(requests).toHaveLength(0);
});
it("keeps Ozon settings and totals bound to the second account instead of the selected first one", async () => {
  const { fetcher } = fixture();
  const second = await card("Второй Ozon");
  expect(within(second).getByText("99")).toBeTruthy();
  expect(within(second).queryByText("22")).toBeNull();
  fireEvent.click(within(second).getByRole("button", { name: "Настройки" }));
  const dialog = await screen.findByRole("dialog", {
    name: "Подключение Ozon",
  });
  await waitFor(() =>
    expect(
      (
        within(dialog).getByLabelText(
          "Название подключения",
        ) as HTMLInputElement
      ).value,
    ).toBe("Второй Ozon"),
  );
  expect(
    fetcher.mock.calls.some(
      ([path]) =>
        String(path).includes("credentials?") &&
        String(path).includes("account=ozon-b"),
    ),
  ).toBe(true);
});
it("offers new Ozon credentials without replacing an existing account", async () => {
  fixture();
  await card("Второй Ozon");
  const group = screen.getByRole("region", { name: "Ozon: аккаунты" });
  fireEvent.click(
    within(group).getByRole("button", { name: /Добавить аккаунт/ }),
  );
  const dialog = await screen.findByRole("dialog", {
    name: "Подключение Ozon",
  });
  expect(
    (within(dialog).getByLabelText("ID клиента Ozon") as HTMLInputElement)
      .value,
  ).toBe("");
});
it("keeps sync setup collapsed until requested and preserves stock inspection", async () => {
  fixture();
  await card("Склад А");
  expect(
    screen.queryByRole("region", { name: "Настройка синхронизации" }),
  ).toBeNull();
  fireEvent.click(
    screen.getByRole("button", { name: "Настройка синхронизации" }),
  );
  const setup = screen.getByRole("region", { name: "Настройка синхронизации" });
  expect(
    within(setup).getByRole("button", { name: "Проверить остатки" }),
  ).toBeTruthy();
  expect(within(setup).getByText("Шаг 3 из 5")).toBeTruthy();
});
it("requires explicit import consent and fences an uncertain submission for the exact account", async () => {
  const { requests } = fixture();
  const second = await card("Второй Ozon");
  fireEvent.click(
    within(second).getByRole("button", { name: "Обновить" }),
  );
  const dialog = await screen.findByRole("dialog", {
    name: "Загрузить данные Ozon?",
  });
  expect(requests).toHaveLength(0);
  fireEvent.click(within(dialog).getByRole("checkbox"));
  fireEvent.click(within(dialog).getByRole("button", { name: "Подтвердить загрузку" }));
  await waitFor(() => expect(requests).toHaveLength(1));
  expect(requests[0].body.ozon_account).toBe("ozon-b");
  expect(requests[0].body.tenant).toBe("merchant");
  expect(
    (
      within(second).getByRole("button", {
        name: "Обновить",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
});
it("does not borrow another account's data after an API failure", async () => {
  fixture({ errorAccount: "ozon-b" });
  const second = await screen.findByRole("article", { name: "Второй Ozon" });
  await within(second).findByText("Состояние недоступно");
  expect(within(second).queryByText("99")).toBeNull();
  expect(within(second).queryByText("22")).toBeNull();
  expect(
    within(second).getByRole("button", { name: "Повторить" }),
  ).toBeTruthy();
  await card("Основной Ozon");
});
it.each(["QUEUED", "RUNNING", "OUTCOME_UNKNOWN"])(
  "keeps an %s import fenced and retains completed snapshot totals",
  async (state) => {
    fixture({ importState: state });
    const second = await screen.findByRole("article", { name: "Второй Ozon" });
    await waitFor(() =>
      expect(
        (
          within(second).getByRole("button", {
            name: "Обновить",
          }) as HTMLButtonElement
        ).disabled,
      ).toBe(true),
    );
    expect(within(second).getByText("99")).toBeTruthy();
  },
);
