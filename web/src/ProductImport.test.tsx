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
function setup(lost = false, count = 0) {
  let savedDraft = {
    revision: 0,
    state: {},
    updated_at: new Date().toISOString(),
  };
  const writes: Array<{ path: string; body: Record<string, unknown> }> = [];
  const fetcher = vi
    .spyOn(globalThis, "fetch")
    .mockImplementation(async (input, init) => {
      const path = new URL(String(input), "http://localhost");
      if (path.pathname === "/api/product-import-draft") {
        if (init?.method === "POST")
          savedDraft = {
            revision: savedDraft.revision + 1,
            state: JSON.parse(String(init.body)).state,
            updated_at: new Date().toISOString(),
          };
        return response(savedDraft);
      }
      if ((!init?.method || init.method === "GET") && count)
        return response({
          products: Array.from({ length: count }, (_, i) =>
            source("SKU-" + String(i).padStart(3, "0")),
          ),
        });
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
          decisions: (body.targets as Enrollment[]).map((e) => ({
            enrollment_id: `frozen-${e.source.id}`,
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
  const scene = (
    <MemoryRouter>
      <QueryClientProvider client={client}>
        <ProductImport scope={scope} provider="ozon" />
      </QueryClientProvider>
    </MemoryRouter>
  );
  const view = render(scene);
  return {
    writes,
    fetcher,
    remount: () => {
      view.rerender(<></>);
      client.clear();
      view.rerender(scene);
    },
  };
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
  expect(writes[1].body.enrollment_ids).toEqual(["frozen-A", "frozen-B"]);
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
    screen.queryByRole("button", { name: "Проверить выбранные (2)" }),
  ).toBeNull();
  expect(
    (
      screen.getByRole("button", {
        name: /Вернуться к готовности товаров/,
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  expect(
    (
      screen.getByRole("button", {
        name: "Отправить изменения (2)",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  expect(writes.filter((w) => w.path.endsWith("import-preview"))).toHaveLength(
    1,
  );
  expect(writes.filter((w) => w.path.endsWith("approve"))).toHaveLength(1);
});

it("imports a selection larger than twenty using immutable per-batch approvals", async () => {
  const { writes, fetcher } = setup(false, 45);
  fireEvent.click(
    await screen.findByRole("button", {
      name: "Выбрать доступные на странице",
    }),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Проверить выбранные (45)" }),
  );
  await screen.findByRole("heading", {
    name: "Готовность карточек к созданию",
  });
  expect(
    writes
      .filter((w) => w.path.endsWith("import-preview"))
      .map((w) => (w.body.targets as Enrollment[]).length),
  ).toEqual([20, 20, 5]);
  fireEvent.click(
    screen.getByRole("button", { name: "Выбрать все готовые (45)" }),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Продолжить с 45 товарами →" }),
  );
  fireEvent.click(
    await screen.findByLabelText(/Я проверил изменения выбранных товаров/),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Отправить изменения (45)" }),
  );
  await screen.findByRole("heading", { name: "Запрос поставлен в обработку" });
  const approved = writes.filter((w) => w.path.endsWith("approve"));
  expect(
    approved.map((w) => (w.body.enrollment_ids as string[]).length),
  ).toEqual([20, 20, 5]);
  expect(new Set(approved.map((w) => w.body.idempotency_key)).size).toBe(3);
  const calls = fetcher.mock.calls;
  const firstApproval = calls.findIndex(([url]) =>
    String(url).endsWith("/approve"),
  );
  expect(
    calls
      .slice(0, firstApproval)
      .some(
        ([url, init]) =>
          String(url).includes("product-import-draft") &&
          init?.body &&
          JSON.parse(String(init.body)).state.intents.length === 3,
      ),
  ).toBe(true);
});
it("restores a server draft after leaving the workflow", async () => {
  const { remount } = setup();
  fireEvent.click(await screen.findByLabelText("Выбрать Товар A"));
  fireEvent.click(screen.getByRole("button", { name: "Сохранить черновик" }));
  await screen.findByText(/Черновик сохранён на сервере/);
  remount();
  fireEvent.click(
    await screen.findByRole("button", { name: "Восстановить черновик (1)" }),
  );
  expect(
    (screen.getByLabelText("Выбрать Товар A") as HTMLInputElement).checked,
  ).toBe(true);
});
