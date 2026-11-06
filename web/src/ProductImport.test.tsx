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
import { ProductImport } from "./ProductImport";
import { scopeBody, type Enrollment } from "./CatalogSyncTypes";

const scope = { tenant: "test", moy: "moy", ozon: "ozon" };
const source = (id: string, lifecycle = "active") => ({
  identity: {
    id,
    entity_type: "product",
    offer_id: id,
    account: { provider: "ozon", id: "ozon" },
  },
  lifecycle,
  fields: { name: { state: "known", data: `Товар ${id}` } },
});
const response = (data: unknown) =>
  new Response(JSON.stringify(data), {
    headers: { "Content-Type": "application/json" },
  });
const clients: QueryClient[] = [];
afterEach(() => {
  cleanup();
  clients.forEach((client) => client.clear());
  vi.restoreAllMocks();
});
function setup(lost = false) {
  const writes: Array<{ path: string; body: Record<string, unknown> }> = [];
  const fetcher = vi
    .spyOn(globalThis, "fetch")
    .mockImplementation(async (input, init) => {
      const path = new URL(String(input), "http://localhost");
      if (!init?.method || init.method === "GET")
        return response({
          products: path.searchParams.get("cursor")
            ? [source("B"), source("C", "archived")]
            : [source("A")],
          next_cursor: path.searchParams.get("cursor") ? "" : "page-two",
        });
      const body = JSON.parse(String(init.body));
      writes.push({ path: path.pathname, body });
      if (path.pathname.endsWith("import-preview"))
        return response({
          id: "preview",
          scope: scopeBody(scope),
          config_revision: 1,
          expires_at: new Date(Date.now() + 60000).toISOString(),
          decisions: (body.targets as Enrollment[]).map((e, index) => ({
            enrollment_id: `frozen-${index}`,
            source: source(e.source.id),
            target: { identity: e.target },
            target_absent: true,
            ready: true,
            validation: [],
            plan: { conflicts: [], changes: [], skipped: [] },
            commands: [
              { action: "create", target: e.target, values: e.create!.values },
            ],
          })),
        });
      if (path.pathname.endsWith("approve")) {
        if (lost) throw new TypeError("lost");
        return response({
          id: "operation",
          scope: scopeBody(scope),
          state: "QUEUED",
          targets: [{}, {}],
        });
      }
      throw new Error(`unexpected mutation ${path.pathname}`);
    });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  clients.push(client);
  render(
    <MemoryRouter>
      <QueryClientProvider client={client}>
        <ProductImport scope={scope} provider="ozon" />
      </QueryClientProvider>
    </MemoryRouter>,
  );
  return { writes, fetcher };
}
async function prepare() {
  fireEvent.click(await screen.findByLabelText("Выбрать Товар A"));
  fireEvent.click(screen.getByRole("button", { name: "Далее" }));
  fireEvent.click(await screen.findByLabelText("Выбрать Товар B"));
  expect(
    (screen.getByLabelText("Выбрать Товар C") as HTMLInputElement).disabled,
  ).toBe(true);
  fireEvent.click(
    screen.getByRole("button", { name: "Проверить выбранные (2)" }),
  );
  await screen.findByRole("heading", {
    name: "Готовность карточек к созданию",
  });
  fireEvent.click(screen.getByLabelText("Выбрать Товар A"));
  fireEvent.click(screen.getByLabelText("Выбрать Товар B"));
  fireEvent.click(
    screen.getByRole("button", { name: "Продолжить с 2 товарами →" }),
  );
  await screen.findByRole("heading", {
    name: "Проверьте изменения перед отправкой",
  });
}
async function confirm() {
  fireEvent.click(
    screen.getByLabelText(/Я проверил изменения выбранных товаров/),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Отправить изменения (2)" }),
  );
}
it("keeps selection across pages and uses one server plan and one explicit approval without changing rules", async () => {
  const { writes } = setup();
  await prepare();
  expect(writes.map((w) => w.path)).toEqual([
    "/api/catalog-sync/import-preview",
  ]);
  expect(
    (writes[0].body.targets as Enrollment[]).map((t) => t.source.id),
  ).toEqual(["A", "B"]);
  expect(
    (
      screen.getByRole("button", {
        name: "Отправить изменения (2)",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  await confirm();
  await screen.findByRole("heading", { name: "Запрос поставлен в обработку" });
  expect(writes.map((w) => w.path)).toEqual([
    "/api/catalog-sync/import-preview",
    "/api/catalog-sync/approve",
  ]);
  expect(writes[1].body.enrollment_ids).toEqual(["frozen-0", "frozen-1"]);
  expect(screen.queryByText("Успешно создано")).toBeNull();
});
it("does not retry or create another plan after an unknown approval outcome", async () => {
  const { writes } = setup(true);
  await prepare();
  await confirm();
  await waitFor(() =>
    expect(screen.getByRole("alert").textContent).toContain(
      "Операция могла сохраниться",
    ),
  );
  expect(
    (
      screen.getByRole("button", {
        name: "Проверить выбранные (2)",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  expect(writes.filter((w) => w.path.endsWith("approve"))).toHaveLength(1);
});
