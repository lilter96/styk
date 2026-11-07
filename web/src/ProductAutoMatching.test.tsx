import { afterEach, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { ProductAutoMatching } from "./ProductAutoMatching";
import type { MatchingJob } from "./ProductMatchingTypes";
const clients: QueryClient[] = [];
afterEach(() => {
  cleanup();
  clients.forEach((c) => c.clear());
  clients.length = 0;
  localStorage.clear();
  vi.restoreAllMocks();
});
const exact = {
  product_id: "101",
  offer_id: "A",
  name: "Кружка синяя",
  status: "exact",
  reason: "exact_sku",
  shared_inventory: false,
  candidates: [
    {
      id: "p1",
      kind: "product",
      name: "Кружка синяя",
      article: "A",
      evidence: "exact_sku",
      score: 1,
    },
  ],
};
const response = (value: unknown) =>
  new Response(JSON.stringify(value), {
    headers: { "Content-Type": "application/json" },
  });
function mount(lost = false, noMatches = false) {
  let j: MatchingJob = {
    id: "job-one",
    criterion: "article_or_code",
    state: "COMPLETED",
    processed: 2105,
    total: 2105,
    pages: 22,
    code: "",
    summary: { exact: noMatches ? 0 : 2104, conflict: 1 },
    examples: noMatches ? [] : [exact],
    attention: [],
    apply: {
      state: "NOT_STARTED",
      total: 0,
      confirmed: 0,
      skipped: 0,
      code: "",
    },
  };
  let policy = {
    revision: 0,
    exact_sku: true,
    names: true,
    auto_confirm: false,
    after_import: false,
  };
  const calls: { path: string; body: Record<string, unknown> }[] = [];
  vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
    const path = new URL(String(input), "http://localhost").pathname;
    const body = init?.method === "POST" ? JSON.parse(String(init.body)) : null;
    if (body) calls.push({ path, body });
    if (path.endsWith("settings")) {
      if (body) policy = { ...body.policy, revision: policy.revision + 1 };
      return response(policy);
    }
    if (path.endsWith("/apply")) {
      if (lost) {
        lost = false;
        throw new TypeError("Connection lost");
      }
      j = {
        ...j,
        apply: {
          state: "COMPLETED",
          total: 2104,
          confirmed: 2104,
          skipped: 0,
          code: "",
        },
      };
      return response({ job_id: j.id });
    }
    if (path.endsWith("/retry")) return new Response(null, { status: 204 });
    if (path.endsWith("/jobs") && body) {
      j = { ...j, criterion: body.criterion, id: "job-" + calls.length };
      return response(j);
    }
    if (path.includes("/jobs/")) return response(j);
    throw new Error("Unexpected API request " + path);
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  clients.push(client);
  const scope = { tenant: "test", moy: "moy", ozon: "ozon" };
  const scene = (s = scope) => (
    <MemoryRouter>
      <QueryClientProvider client={client}>
        <ProductAutoMatching scope={s} />
      </QueryClientProvider>
    </MemoryRouter>
  );
  const view = render(scene());
  return {
    calls,
    remount: () => {
      view.rerender(<></>);
      client.clear();
      view.rerender(scene());
    },
    changeScope: () => view.rerender(scene({ ...scope, ozon: "another-shop" })),
  };
}
it("shows examples automatically and applies every exact pair across the whole catalogue in one click", async () => {
  const { calls } = mount();
  const apply = await screen.findByRole("button", {
    name: "Сопоставить все товары (2104)",
  });
  expect(
    screen.getByRole("region", { name: "Примеры найденных совпадений" }),
  ).toBeTruthy();
  expect(screen.queryByRole("button", { name: /Следующие товары/ })).toBeNull();
  expect(calls.map((c) => c.path)).toEqual(["/api/product-matching/jobs"]);
  fireEvent.click(apply);
  await screen.findByRole("heading", { name: "Сопоставлено товаров: 2104" });
  const approvals = calls.filter((c) => c.path.endsWith("/apply"));
  expect(approvals).toHaveLength(1);
  expect(approvals[0].body).toMatchObject({
    expected_count: 2104,
    acknowledge: true,
  });
  expect(approvals[0].body).not.toHaveProperty("product_ids");
  expect(calls.some((c) => c.path.endsWith("settings"))).toBe(false);
});
it("a different criterion creates its own read-only search without saving automation", async () => {
  const { calls } = mount();
  await screen.findByRole("button", { name: "Сопоставить все товары (2104)" });
  fireEvent.click(screen.getByRole("radio", { name: /Только код/ }));
  await waitFor(() =>
    expect(calls.filter((c) => c.path.endsWith("/jobs"))).toHaveLength(2),
  );
  expect(calls.at(-1)?.body.criterion).toBe("code");
  expect(
    calls.every(
      (c) => !c.path.endsWith("/apply") && !c.path.endsWith("settings"),
    ),
  ).toBe(true);
});
it("restores an uncertain approval and repeats its exact key after reload", async () => {
  const { calls, remount } = mount(true);
  fireEvent.click(
    await screen.findByRole("button", {
      name: "Сопоставить все товары (2104)",
    }),
  );
  await screen.findByText(/Ответ о запуске не получен/);
  const first = calls.find((c) => c.path.endsWith("/apply"))!.body;
  remount();
  fireEvent.click(
    await screen.findByRole("button", {
      name: "Проверить запуск сопоставления",
    }),
  );
  await screen.findByRole("heading", { name: "Сопоставлено товаров: 2104" });
  const approvals = calls.filter((c) => c.path.endsWith("/apply"));
  expect(approvals).toHaveLength(2);
  expect(approvals[1].body).toEqual(first);
  expect(calls.filter((c) => c.path.endsWith("/jobs"))).toHaveLength(1);
});
it("does not offer a bulk confirmation when no exact pairs were found", async () => {
  const { calls } = mount(false, true);
  await screen.findByRole("heading", { name: "Точных совпадений пока нет" });
  expect(
    (
      screen.getByRole("button", {
        name: "Сопоставить все товары (0)",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  expect(calls.some((c) => c.path.endsWith("/apply"))).toBe(false);
});
it("saves standing automation only through its separate explicit action", async () => {
  const { calls } = mount();
  await screen.findByRole("button", { name: "Сопоставить все товары (2104)" });
  fireEvent.click(
    screen.getByText("Сопоставлять товары после новых загрузок", {
      selector: "summary",
    }),
  );
  fireEvent.click(
    screen.getByLabelText(
      "Автоматически сопоставлять точные пары после новых полных загрузок",
    ),
  );
  expect(calls.some((c) => c.path.endsWith("settings"))).toBe(false);
  fireEvent.click(
    screen.getByRole("button", { name: "Сохранить автоматизацию" }),
  );
  await screen.findByText("Автоматизация сохранена");
  expect(
    calls.find((c) => c.path.endsWith("settings"))?.body.policy,
  ).toMatchObject({ after_import: true, auto_confirm: true });
});
it("switching accounts starts a separate search and cannot carry a previous approval", async () => {
  const { calls, changeScope } = mount();
  await screen.findByRole("button", { name: "Сопоставить все товары (2104)" });
  changeScope();
  await waitFor(() =>
    expect(calls.filter((c) => c.path.endsWith("/jobs"))).toHaveLength(2),
  );
  expect(calls.at(-1)?.body.ozon_account).toBe("another-shop");
  expect(calls.some((c) => c.path.endsWith("/apply"))).toBe(false);
});
