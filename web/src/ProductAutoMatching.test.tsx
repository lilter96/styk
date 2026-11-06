import { afterEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { ProductAutoMatching } from "./ProductAutoMatching";

const clients: QueryClient[] = [];
afterEach(() => {
  cleanup();
  clients.forEach((c) => c.clear());
  vi.restoreAllMocks();
});
function mount(lost = false) {
  const calls: Array<{ path: string; body: Record<string, unknown> }> = [];
  let policy = {
    revision: 0,
    exact_sku: true,
    names: true,
    auto_confirm: false,
    after_import: false,
  };
  let attempts = 0;
  const items = [
    {
      product_id: "1",
      offer_id: "A",
      name: "Чашка",
      status: "exact",
      reason: "unique_exact",
      candidates: [
        {
          kind: "product",
          id: "p1",
          name: "Чашка",
          article: "A",
          evidence: "exact_sku",
        },
      ],
    },
    {
      product_id: "2",
      offer_id: "B",
      name: "Чашка большая",
      status: "review",
      reason: "similar_name",
      candidates: [
        {
          kind: "product",
          id: "p1",
          name: "Чашка",
          article: "A",
          evidence: "similar_name",
        },
      ],
    },
  ];
  vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
    const path = new URL(String(input), "http://localhost").pathname;
    const response = (data: unknown) =>
      new Response(JSON.stringify(data), {
        headers: { "Content-Type": "application/json" },
      });
    if (!init?.method || init.method === "GET") return response(policy);
    const body = JSON.parse(String(init.body));
    calls.push({ path, body });
    if (path.endsWith("settings")) {
      policy = { ...body.policy, revision: 1 };
      return response(policy);
    }
    if (path.endsWith("preview"))
      return response({
        id: "preview",
        scope: {
          tenant: "test",
          moysklad_account: "moy",
          ozon_account: "ozon",
        },
        policy,
        items,
        expires_at: new Date(Date.now() + 60000).toISOString(),
      });
    if (path.endsWith("runs")) {
      if (lost && ++attempts === 1) throw new TypeError("lost response");
      return response({
        id: "run",
        preview_id: "preview",
        scope: {
          tenant: "test",
          moysklad_account: "moy",
          ozon_account: "ozon",
        },
        confirmed: 1,
        items: [
          { ...items[0], status: "confirmed", reason: "confirmed_by_rule" },
        ],
      });
    }
    throw new Error(path);
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  clients.push(client);
  render(
    <MemoryRouter>
      <QueryClientProvider client={client}>
        <ProductAutoMatching
          scope={{ tenant: "test", moy: "moy", ozon: "ozon" }}
        />
      </QueryClientProvider>
    </MemoryRouter>,
  );
  return calls;
}

it("suggests by default without saving a policy or allowing a name match to confirm", async () => {
  const calls = mount();
  fireEvent.click(
    await screen.findByRole("button", {
      name: "Посмотреть предварительный результат",
    }),
  );
  expect(
    ((await screen.findByLabelText("Выбрать пару A")) as HTMLInputElement)
      .disabled,
  ).toBe(true);
  expect(
    (screen.getByLabelText("Выбрать пару B") as HTMLInputElement).disabled,
  ).toBe(true);
  expect(calls.map((c) => c.path)).toEqual(["/api/product-matching/preview"]);
});

it("requires opt-in, exact selection and acknowledgement, and recovers a lost response with the same key", async () => {
  const calls = mount(true);
  fireEvent.click(
    await screen.findByRole("radio", {
      name: /Разрешить подтверждение уникальных/,
    }),
  );
  fireEvent.click(
    screen.getByRole("button", {
      name: "Посмотреть предварительный результат",
    }),
  );
  fireEvent.click(await screen.findByLabelText("Выбрать пару A"));
  expect(
    (screen.getByLabelText("Выбрать пару B") as HTMLInputElement).disabled,
  ).toBe(true);
  expect(
    (
      screen.getByRole("button", {
        name: "Подтвердить выбранные пары",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  fireEvent.click(
    screen.getByLabelText(/Разрешаю подтвердить выбранные точные пары/),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Подтвердить выбранные пары" }),
  );
  const retry = await screen.findByRole("button", {
    name: "Проверить результат",
  });
  expect(
    (screen.getByLabelText("Выбрать пару A") as HTMLInputElement).disabled,
  ).toBe(true);
  fireEvent.click(retry);
  await screen.findByText(/Подтверждено в этом запуске: 1/);
  const runs = calls.filter((c) => c.path.endsWith("runs"));
  expect(runs).toHaveLength(2);
  expect(runs[0].body).toEqual(runs[1].body);
  expect(runs[0].body.product_ids).toEqual(["1"]);
});
