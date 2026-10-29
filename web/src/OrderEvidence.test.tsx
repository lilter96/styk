import { afterEach, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { OrderEvidence } from "./OrderEvidence";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});
it("shows observed reserve without treating missing order coverage as zero", async () => {
  const calls: { method: string; body?: string }[] = [];
  vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
    calls.push({ method: init?.method || "GET", body: init?.body?.toString() });
    const response =
      init?.method === "POST"
        ? { source_status: "LIMIT_REACHED", posting_status: "COMPLETED" }
        : {
            source_batch_id: 1,
            source_from: "2026-10-29T10:00:00Z",
            source_to: "2026-10-29T11:00:00Z",
            source_checked_at: "2026-10-29T11:00:01Z",
            positions: [
              {
                order_id: "synthetic-order",
                position_id: "p1",
                quantity: "3",
                reserve: null,
                shipped: "0",
                applicable: true,
                observed_at: "2026-10-29T11:00:01Z",
              },
            ],
            postings: [],
            truncated: false,
          };
    return new Response(JSON.stringify(response), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <OrderEvidence
        scope={{ tenant: "t", moysklad_account: "m", ozon_account: "o" }}
        product="1"
        warehouse="2"
      />
    </QueryClientProvider>,
  );
  fireEvent.click(screen.getByText("Заказы и резервы"));
  expect(await screen.findByText(/резерв неизвестно/)).toBeTruthy();
  expect(
    screen.getByText(/не доказывает отсутствие обязательств/),
  ).toBeTruthy();
  fireEvent.change(screen.getByLabelText("Период проверки"), {
    target: { value: "24" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Проверить заказы" }));
  await screen.findByText(
    /Неполные данные не сохранены как завершённая проверка/,
  );
  await waitFor(() =>
    expect(
      calls.some(
        (c) =>
          c.method === "POST" && JSON.parse(c.body || "{}").window_hours === 24,
      ),
    ).toBe(true),
  );
  client.clear();
});

it("restores an incomplete run and resumes the same period explicitly", async () => {
  const requests: number[] = [];
  vi.spyOn(globalThis, "fetch").mockImplementation(async (_input, init) => {
    let data: unknown = {
      positions: [],
      postings: [],
      truncated: false,
      progress: {
        window_hours: 24,
        from: "2026-10-28T10:00:00Z",
        to: "2026-10-29T10:00:00Z",
        source_status: "IN_PROGRESS",
        posting_status: "COMPLETED",
        source_completed_windows: 2,
        source_pending_windows: 3,
      },
    };
    if (init?.method === "POST") {
      requests.push(JSON.parse(String(init.body)).window_hours);
      data = { source_status: "COMPLETED", posting_status: "COMPLETED" };
    }
    return new Response(JSON.stringify(data), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <OrderEvidence
        scope={{ tenant: "t", moysklad_account: "m", ozon_account: "o" }}
        product="1"
        warehouse="2"
      />
    </QueryClientProvider>,
  );
  fireEvent.click(screen.getByText("Заказы и резервы"));
  await screen.findByText(/Завершено интервалов: 2; осталось: 3/);
  expect(
    screen.getByText(/неполная проверка не подтверждает полноту данных/),
  ).toBeTruthy();
  expect(requests).toEqual([]);
  fireEvent.click(screen.getByRole("button", { name: "Продолжить проверку" }));
  await screen.findByText(
    "Проверка обоих сервисов за выбранный период завершена.",
  );
  expect(requests).toEqual([24]);
  client.clear();
});

it("warns that changing the period replaces unfinished progress", async () => {
  const fetcher = vi.spyOn(globalThis, "fetch").mockResolvedValue(
    new Response(
      JSON.stringify({
        positions: [],
        postings: [],
        truncated: false,
        progress: {
          window_hours: 24,
          from: "2026-10-28T10:00:00Z",
          to: "2026-10-29T10:00:00Z",
          source_status: "IN_PROGRESS",
          posting_status: "COMPLETED",
        },
      }),
      { status: 200, headers: { "Content-Type": "application/json" } },
    ),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <OrderEvidence
        scope={{ tenant: "t", moysklad_account: "m", ozon_account: "o" }}
        product="1"
        warehouse="2"
      />
    </QueryClientProvider>,
  );
  fireEvent.click(screen.getByText("Заказы и резервы"));
  await screen.findByRole("button", { name: "Продолжить проверку" });
  fireEvent.change(screen.getByLabelText("Период проверки"), {
    target: { value: "1" },
  });
  expect(screen.getByText(/заменит незавершённый прогресс/)).toBeTruthy();
  expect(
    screen.getByRole("button", { name: "Начать новую проверку" }),
  ).toBeTruthy();
  expect(
    fetcher.mock.calls.filter(([, init]) => init?.method === "POST"),
  ).toHaveLength(0);
  client.clear();
});

it("does not offer continuation for an expired run", async () => {
  vi.spyOn(globalThis, "fetch").mockResolvedValue(
    new Response(
      JSON.stringify({
        positions: [],
        postings: [],
        truncated: false,
        progress: {
          window_hours: 1,
          from: "2026-10-28T10:00:00Z",
          to: "2026-10-28T11:00:00Z",
          source_status: "IN_PROGRESS",
          posting_status: "COMPLETED",
          expired: true,
        },
      }),
      { status: 200, headers: { "Content-Type": "application/json" } },
    ),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <OrderEvidence
        scope={{ tenant: "t", moysklad_account: "m", ozon_account: "o" }}
        product="1"
        warehouse="2"
      />
    </QueryClientProvider>,
  );
  fireEvent.click(screen.getByText("Заказы и резервы"));
  await screen.findByText(/Срок незавершённой проверки истёк/);
  expect(
    screen.getByRole("button", { name: "Начать новую проверку" }),
  ).toBeTruthy();
  expect(
    screen.queryByRole("button", { name: "Продолжить проверку" }),
  ).toBeNull();
  client.clear();
});

it("refreshes persisted progress after an unknown request outcome without retrying", async () => {
  let gets = 0;
  let posts = 0;
  vi.spyOn(globalThis, "fetch").mockImplementation(async (_input, init) => {
    if (init?.method === "POST") {
      posts++;
      throw new Error("synthetic lost response");
    }
    gets++;
    return new Response(
      JSON.stringify({
        positions: [],
        postings: [],
        truncated: false,
        progress: {
          window_hours: 1,
          from: "2026-10-28T10:00:00Z",
          to: "2026-10-28T11:00:00Z",
          source_status: gets > 1 ? "LIMIT_REACHED" : "IN_PROGRESS",
          source_limit_resource: gets > 1 ? "staging_bytes" : "",
          posting_status: "COMPLETED",
        },
      }),
      { status: 200, headers: { "Content-Type": "application/json" } },
    );
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <OrderEvidence
        scope={{ tenant: "t", moysklad_account: "m", ozon_account: "o" }}
        product="1"
        warehouse="2"
      />
    </QueryClientProvider>,
  );
  fireEvent.click(screen.getByText("Заказы и резервы"));
  fireEvent.click(
    await screen.findByRole("button", { name: "Продолжить проверку" }),
  );
  await screen.findByText(/Ограничение: объём временных данных/);
  expect(
    screen.getByText(/неполная проверка не подтверждает полноту данных/),
  ).toBeTruthy();
  expect(
    screen.getByRole("button", { name: "Начать новую проверку" }),
  ).toBeTruthy();
  expect(posts).toBe(1);
  client.clear();
});

it("explains that a conflict starts a new run", async () => {
  vi.spyOn(globalThis, "fetch").mockResolvedValue(
    new Response(
      JSON.stringify({
        positions: [],
        postings: [],
        truncated: false,
        progress: {
          window_hours: 1,
          from: "2026-10-28T10:00:00Z",
          to: "2026-10-28T11:00:00Z",
          source_status: "CONFLICT",
          posting_status: "COMPLETED",
        },
      }),
      { status: 200, headers: { "Content-Type": "application/json" } },
    ),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <OrderEvidence
        scope={{ tenant: "t", moysklad_account: "m", ozon_account: "o" }}
        product="1"
        warehouse="2"
      />
    </QueryClientProvider>,
  );
  fireEvent.click(screen.getByText("Заказы и резервы"));
  await screen.findByText(/Обнаружен конфликт данных/);
  expect(
    screen.getByRole("button", { name: "Начать новую проверку" }),
  ).toBeTruthy();
  expect(
    screen.queryByRole("button", { name: "Продолжить проверку" }),
  ).toBeNull();
  client.clear();
});
