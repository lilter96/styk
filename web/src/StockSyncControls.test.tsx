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
import { useState } from "react";
import {
  StockSyncControls,
  StockSyncNotice,
  useStockSyncSubmission,
  type StockSyncView,
} from "./StockSyncControls";

const scope = {
  tenant: "test-tenant",
  moysklad_account: "moy-account",
  ozon_account: "ozon-account",
};
const clients: QueryClient[] = [];
afterEach(() => {
  cleanup();
  for (const client of clients) client.clear();
  clients.length = 0;
  vi.restoreAllMocks();
});
function setup({
  ready = true,
  lost = false,
  blocked = false,
  statusFailure = false,
  expired = false,
  remoteState = "",
  deploymentUnavailable = false,
  omitAvailability = false,
  testAccount = false,
  attestationFails = false,
}: {
  ready?: boolean;
  lost?: boolean;
  blocked?: boolean;
  statusFailure?: boolean;
  expired?: boolean;
  remoteState?: string;
  deploymentUnavailable?: boolean;
  omitAvailability?: boolean;
  testAccount?: boolean;
  attestationFails?: boolean;
} = {}) {
  let accepted = 0;
  const calls: { path: string; method: string; body: string }[] = [];
  const target = {
    ozon_product_id: "101",
    ozon_warehouse_id: "201",
    source_item_id: "moy-item",
    source_warehouse_id: "moy-warehouse",
    quantity: ready ? "5" : undefined,
    status: ready ? "READY_CONDITIONAL" : "UNKNOWN",
    checks: ready
      ? []
      : [
          {
            code: "SALE_UNIT_UNVERIFIED",
            passed: false,
            message: "proof missing",
          },
        ],
    product_mapping_version: 1,
    warehouse_mapping_version: 1,
  };
  const view = (selected = scope): StockSyncView => ({
    ...selected,
    ...(testAccount ? { test_account_mode: true } : {}),
    ...(omitAvailability
      ? {}
      : { publication_available: !deploymentUnavailable }),
    enabled:
      blocked || (accepted > 0 && selected.ozon_account === scope.ozon_account),
    state:
      remoteState ||
      (blocked
        ? "BLOCKED"
        : accepted && selected.ozon_account === scope.ozon_account
          ? "QUEUED"
          : "DISABLED"),
    ready: blocked ? false : ready,
    checks: [],
    targets: [target],
    ...(blocked
      ? {
          last_operation: {
            ...selected,
            id: "p".repeat(32),
            state: "SUCCEEDED",
            created_at: new Date().toISOString(),
            targets: [],
          },
          last_confirmed_at: "2026-01-01T00:00:00Z",
        }
      : {}),
  });
  vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
    const path = String(input),
      method = init?.method || "GET",
      body = String(init?.body || "");
    calls.push({ path, method, body });
    if (method === "GET") {
      if (statusFailure)
        return new Response('{"error":"STOCK_SYNC_UNAVAILABLE"}', {
          status: 503,
        });
      const url = new URL(path, "http://test.local");
      return new Response(
        JSON.stringify(
          view({
            tenant: url.searchParams.get("tenant")!,
            moysklad_account: url.searchParams.get("moysklad_account")!,
            ozon_account: url.searchParams.get("ozon_account")!,
          }),
        ),
        { status: 200 },
      );
    }
    const value = JSON.parse(body);
    if (path.endsWith("/preview") && value.attestations && attestationFails)
      return new Response('{"error":"STOCK_SYNC_UNAVAILABLE"}', {
        status: 503,
      });
    if (path.endsWith("/preview"))
      return new Response(
        JSON.stringify({
          ...value,
          review_id: "r".repeat(32),
          ready,
          checks: ready
            ? []
            : [
                {
                  code: "MAPPINGS_REQUIRED",
                  passed: false,
                  message: "mapping absent",
                },
              ],
          targets: [target],
          created_at: new Date().toISOString(),
          expires_at: new Date(
            Date.now() + (expired ? -1000 : 60000),
          ).toISOString(),
        }),
        { status: 200 },
      );
    if (path.endsWith("/enable")) {
      accepted++;
      if (lost && accepted === 1) throw new TypeError("response lost");
      return new Response(JSON.stringify(view()), { status: 200 });
    }
    if (path.endsWith("/disable"))
      return new Response(
        JSON.stringify({ ...view(), enabled: false, state: "DISABLED" }),
        { status: 200 },
      );
    if (path.endsWith("/reconcile"))
      return new Response(
        JSON.stringify({
          ...view(),
          enabled: false,
          state: "DISABLED",
          last_operation: {
            ...scope,
            id: "p".repeat(32),
            state: "CONFIRMED_EFFECTIVE",
            created_at: new Date().toISOString(),
            targets: [],
          },
        }),
        { status: 200 },
      );
    throw new Error("Unexpected test endpoint");
  });
  function Harness() {
    const submission = useStockSyncSubmission();
    const [selected, setSelected] = useState(scope);
    return (
      <>
        <StockSyncNotice
          submission={submission}
          scope={selected}
          onIntegrations
          restore={setSelected}
        />
        <StockSyncControls
          tenant={selected.tenant}
          moy={selected.moysklad_account}
          ozon={selected.ozon_account}
          eligible
          submission={submission}
        />
        <button
          onClick={() =>
            setSelected({ ...scope, ozon_account: "another-ozon" })
          }
        >
          Другой аккаунт
        </button>
      </>
    );
  }
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  clients.push(client);
  render(
    <QueryClientProvider client={client}>
      <Harness />
    </QueryClientProvider>,
  );
  return { calls };
}
async function openEnable() {
  const action = await screen.findByRole("button", {
    name: "Включить синхронизацию",
  });
  await waitFor(() =>
    expect((action as HTMLButtonElement).disabled).toBe(false),
  );
  fireEvent.click(action);
  const dialog = await screen.findByRole("dialog", {
    name: "Включить синхронизацию остатков?",
  });
  await within(dialog).findByLabelText(
    "Подтверждаю изменение остатков в Ozon для выбранных аккаунтов",
  );
  return dialog;
}
it("requires a readiness review and explicit acknowledgement before enabling external writes", async () => {
  const { calls } = setup();
  await screen.findByRole("button", { name: "Включить синхронизацию" });
  expect(calls.every((call) => call.method === "GET")).toBe(true);
  const dialog = await openEnable();
  await within(dialog).findByText(/Условия отправки подтверждены/);
  const confirm = within(dialog).getByRole("button", {
    name: "Включить синхронизацию",
  }) as HTMLButtonElement;
  expect(confirm.disabled).toBe(true);
  expect(calls.filter((call) => call.path.endsWith("/enable"))).toHaveLength(0);
  fireEvent.click(within(dialog).getByRole("checkbox"));
  fireEvent.click(confirm);
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  const sent = calls.filter((call) => call.path.endsWith("/enable"));
  expect(sent).toHaveLength(1);
  expect(JSON.parse(sent[0].body)).toMatchObject({
    ...scope,
    review_id: "r".repeat(32),
    acknowledge: true,
  });
  expect(JSON.parse(sent[0].body).idempotency_key).toMatch(/^[a-f0-9-]{36}$/);
  expect(sent[0].body).not.toContain("quantity");
});
it("keeps missing readiness blocked even when the user acknowledges the change", async () => {
  const { calls } = setup({ ready: false });
  const dialog = await openEnable();
  await within(dialog).findByText("Отправка пока недоступна");
  fireEvent.click(
    within(dialog).getByLabelText(
      "Подтверждаю изменение остатков в Ozon для выбранных аккаунтов",
    ),
  );
  expect(
    (
      within(dialog).getByRole("button", {
        name: "Включить синхронизацию",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  expect(calls.filter((call) => call.path.endsWith("/enable"))).toHaveLength(0);
});
it("replays the exact enabling intent after a lost response and account switch", async () => {
  const { calls } = setup({ lost: true });
  const dialog = await openEnable();
  await within(dialog).findByText(/Условия отправки подтверждены/);
  fireEvent.click(within(dialog).getByRole("checkbox"));
  fireEvent.click(
    within(dialog).getByRole("button", { name: "Включить синхронизацию" }),
  );
  await within(dialog).findByText(/Результат запроса неизвестен/);
  fireEvent.click(within(dialog).getByRole("button", { name: "Закрыть" }));
  fireEvent.click(screen.getByRole("button", { name: "Другой аккаунт" }));
  await screen.findByText("Есть запрос на изменение синхронизации");
  expect(
    (
      screen.getByRole("button", {
        name: "Включить синхронизацию",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  fireEvent.click(screen.getByRole("button", { name: "К исходным аккаунтам" }));
  fireEvent.click(
    await screen.findByRole("button", { name: "Повторить тот же запрос" }),
  );
  await waitFor(() =>
    expect(calls.filter((call) => call.path.endsWith("/enable"))).toHaveLength(
      2,
    ),
  );
  const sent = calls.filter((call) => call.path.endsWith("/enable"));
  expect(sent[1].body).toBe(sent[0].body);
  expect(JSON.parse(sent[1].body).ozon_account).toBe(scope.ozon_account);
  expect(JSON.stringify({ ...localStorage, ...sessionStorage })).not.toContain(
    JSON.parse(sent[0].body).idempotency_key,
  );
});
it("does not show a past successful publication as currently healthy after conditions expire", async () => {
  setup({ blocked: true });
  await screen.findByText("Приостановлена");
  expect(screen.getByText(/Новые отправки приостановлены/)).toBeTruthy();
  expect(screen.getByText("Приостановлена").className).toContain("warning");
  expect(screen.queryByText("Включена")).toBeNull();
});
it("keeps unspecified quantities and proofs unknown until explicitly provided", async () => {
  const { calls } = setup({ ready: false });
  const dialog = await openEnable();
  await within(dialog).findByText("Отправка пока недоступна");
  fireEvent.click(
    within(dialog).getByRole("button", { name: "Подтвердить условия вручную" }),
  );
  const unseen = within(dialog).getByLabelText(
    "Запас на ещё не учтённые уменьшения, шт.",
  ) as HTMLInputElement;
  expect(unseen.value).toBe("");
  fireEvent.click(
    within(dialog).getByRole("button", {
      name: "Проверить подтверждённые условия",
    }),
  );
  await within(dialog).findByText(/Пустые значения не считаются нулём/);
  expect(calls.filter((call) => call.path.endsWith("/preview"))).toHaveLength(
    1,
  );
  for (const label of [
    "Ещё не учтённые обязательства, шт.",
    "Запас на ещё не учтённые уменьшения, шт.",
    "Страховой остаток, шт.",
  ])
    fireEvent.change(within(dialog).getByLabelText(label), {
      target: { value: "0" },
    });
  fireEvent.change(
    within(dialog).getByLabelText("На какое время подтверждены условия"),
    { target: { value: "2026-01-01T00:00" } },
  );
  fireEvent.change(
    within(dialog).getByLabelText("Основание проверки без ключей доступа"),
    {
      target: { value: "Проверено по независимой сверке исходных документов" },
    },
  );
  fireEvent.click(
    within(dialog).getByLabelText(
      "Единица продажи — одна штука; комплектов и пересчёта единиц нет",
    ),
  );
  fireEvent.click(
    within(dialog).getByRole("button", {
      name: "Проверить подтверждённые условия",
    }),
  );
  await waitFor(() =>
    expect(calls.filter((call) => call.path.endsWith("/preview"))).toHaveLength(
      2,
    ),
  );
  const sent = JSON.parse(
    calls.filter((call) => call.path.endsWith("/preview"))[1].body,
  );
  expect(sent.attestations[0]).toMatchObject({
    unreflected: "0",
    unseen_bound: "0",
    safety_stock: "0",
  });
  expect(sent.attestations[0].proofs).toHaveLength(1);
  expect(sent.attestations[0].proofs[0].code).toBe("SALE_UNIT_UNVERIFIED");
});
it("records explicit test assumptions without calling them independent proof", async () => {
  const { calls } = setup({ ready: false, testAccount: true });
  const dialog = await openEnable();
  await within(dialog).findByText(/Полнота заказов/);
  const button = within(dialog).getByRole("button", {
    name: "Записать тестовые допущения",
  });
  expect((button as HTMLButtonElement).disabled).toBe(true);
  fireEvent.click(
    within(dialog).getByLabelText(/Подтверждаю эти тестовые допущения/),
  );
  fireEvent.click(button);
  await waitFor(() =>
    expect(calls.filter((call) => call.path.endsWith("/preview"))).toHaveLength(
      2,
    ),
  );
  const sent = JSON.parse(
    calls.filter((call) => call.path.endsWith("/preview"))[1].body,
  );
  expect(sent.attestations).toHaveLength(1);
  expect(sent.attestations[0]).toMatchObject({
    unreflected: "0",
    unseen_bound: "0",
    safety_stock: "0",
  });
  expect(sent.attestations[0].proofs).toHaveLength(9);
  expect(sent.attestations[0].proofs[0].reference).toContain(
    "OPERATOR_ATTESTED",
  );
});
it("does not claim test assumptions were saved after a failed review", async () => {
  setup({ ready: false, testAccount: true, attestationFails: true });
  const dialog = await openEnable();
  fireEvent.click(
    within(dialog).getByLabelText(/Подтверждаю эти тестовые допущения/),
  );
  fireEvent.click(
    within(dialog).getByRole("button", { name: "Записать тестовые допущения" }),
  );
  await within(dialog).findByText(/Не удалось проверить условия отправки/);
  expect(within(dialog).queryByText(/Допущения записаны/)).toBeNull();
});
it("rejects expired reviews and unavailable state without creating an enable request", async () => {
  const { calls } = setup({ expired: true });
  const dialog = await openEnable();
  await within(dialog).findByText(/Условия отправки подтверждены/);
  fireEvent.click(within(dialog).getByRole("checkbox"));
  expect(
    (
      within(dialog).getByRole("button", {
        name: "Включить синхронизацию",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  expect(calls.filter((call) => call.path.endsWith("/enable"))).toHaveLength(0);
});
it("does not allow enabling when the current scoped state cannot be loaded", async () => {
  const { calls } = setup({ statusFailure: true });
  await screen.findByText(/Не удалось уточнить состояние отправки/);
  expect(
    (
      screen.getByRole("button", {
        name: "Включить синхронизацию",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  expect(calls.every((call) => call.method === "GET")).toBe(true);
});

it("shows observed stock as requiring manual settlement, never a confirmed completed request", async () => {
  const { calls } = setup({ remoteState: "EFFECT_CONFIRMED_REVIEW_REQUIRED" });
  await screen.findByText("Нужна проверка перед включением");
  expect(
    screen.getByText(
      /Это не подтверждает актуальность ответа или завершение прежнего запроса/,
    ),
  ).toBeTruthy();
  expect(screen.queryByText(/Последнее подтверждённое обновление/)).toBeNull();
  expect(calls.every((c) => c.method === "GET")).toBe(true);
});
it("prevents a new enable action while the preceding publication is unknown", async () => {
  const { calls } = setup({ remoteState: "DISABLED_OUTCOME_UNKNOWN" });
  await screen.findByText("Результат уточняется");
  expect(
    (
      screen.getByRole("button", {
        name: "Включить синхронизацию",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  expect(calls.every((c) => c.method === "GET")).toBe(true);
});
it("offers a fresh review to renew expired enabled conditions without automatically enabling", async () => {
  const { calls } = setup({ blocked: true });
  fireEvent.click(
    await screen.findByRole("button", {
      name: "Проверить условия",
    }),
  );
  await screen.findByRole("dialog", {
    name: "Включить синхронизацию остатков?",
  });
  await waitFor(() =>
    expect(calls.filter((c) => c.path.endsWith("/preview"))).toHaveLength(1),
  );
  expect(calls.filter((c) => c.path.endsWith("/enable"))).toHaveLength(0);
});

it("shows deployment safety hold and prevents new enable", async () => {
  const { calls } = setup({ deploymentUnavailable: true });
  await screen.findByText("Недоступна");
  expect(
    screen.getByText(/проверка безопасности ещё не завершена/),
  ).toBeTruthy();
  expect(
    (
      screen.getByRole("button", {
        name: "Включить синхронизацию",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  expect(calls.every((call) => call.method === "GET")).toBe(true);
});

it("shows the deployment hold for an existing enabled policy and keeps disable available", async () => {
  const { calls } = setup({ blocked: true, deploymentUnavailable: true });
  await screen.findByText("Недоступна");
  expect(screen.queryByText("Включена")).toBeNull();
  expect(screen.getByRole("button", { name: "Выключить" })).toBeTruthy();
  expect(calls.every((call) => call.method === "GET")).toBe(true);
});

it("treats a missing deployment availability field as unavailable", async () => {
  setup({ omitAvailability: true });
  await screen.findByText("Недоступна");
  expect(
    (
      screen.getByRole("button", {
        name: "Включить синхронизацию",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
});

it("keeps the uncertain readback warning visible during the deployment hold", async () => {
  setup({
    remoteState: "EFFECT_CONFIRMED_REVIEW_REQUIRED",
    deploymentUnavailable: true,
  });
  await screen.findByText("Нужна проверка перед включением");
  expect(
    screen.getByText(
      /Это не подтверждает актуальность ответа или завершение прежнего запроса/,
    ),
  ).toBeTruthy();
  expect(
    (
      screen.getByRole("button", {
        name: "Включить синхронизацию",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
});

it("warns that a previously dispatched request can complete during the deployment hold", async () => {
  setup({ remoteState: "STOPPING", deploymentUnavailable: true });
  await screen.findByText("Останавливается");
  expect(
    screen.getByText(
      /Ранее отправленный запрос ещё обрабатывается и может изменить остатки/,
    ),
  ).toBeTruthy();
  expect(
    (
      screen.getByRole("button", {
        name: "Включить синхронизацию",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
});
