import { useState } from "react";
import { afterEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { WarehouseReview } from "./WarehouseReview";
import { ConfirmationNotice, useConfirmation } from "./Confirmation";
const original = { tenant: "t", moy: "m1", ozon: "o1" };
const id = "a".repeat(32);
const base = {
  id: "wh",
  name: "Synthetic",
  account: "account",
  status: "enabled",
  is_rfbs: false,
  observed_at: "2026-01-01T00:00:00Z",
  stock_rows: 0,
};
function Harness() {
  const [scope, setScope] = useState(original);
  const c = useConfirmation();
  return (
    <>
      <button onClick={() => setScope({ tenant: "t", moy: "m2", ozon: "o2" })}>
        Switch accounts
      </button>
      <ConfirmationNotice confirmation={c} scope={scope} restore={setScope} />
      <WarehouseReview
        key={scope.ozon}
        scope={scope}
        moy="m"
        ozon="o"
        confirmation={c}
      />
    </>
  );
}
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});
function setup(status = "CURRENT", loseResponse = false, rejection = "") {
  let confirmed = false;
  let count = 0;
  const mock = vi
    .spyOn(globalThis, "fetch")
    .mockImplementation(async (input, init) => {
      const path = String(input);
      if (path.endsWith("/confirm")) {
        count++;
        if (loseResponse && count === 1) throw new TypeError("offline");
        if (rejection)
          return new Response(JSON.stringify({ error: rejection }), {
            status: rejection === "UNAUTHORIZED" ? 401 : 409,
          });
        confirmed = true;
        return new Response(
          JSON.stringify({
            mapping_id: 7,
            mapping_version: 1,
            review_id: id,
            evidence_class: "OPERATOR_ATTESTED",
            confirmed_at: "2026-01-01T00:00:00Z",
            idempotent_replay: loseResponse,
          }),
          { status: 200 },
        );
      }
      if (path.includes("warehouse-mapping-reviews"))
        return new Response(
          JSON.stringify({
            review: {
              id,
              tenant: "t",
              moysklad_account: "m1",
              ozon_account: "o1",
              moysklad_warehouse_id: "m",
              ozon_warehouse_id: "o",
              moysklad_import_id: 1,
              ozon_import_id: 2,
              created_at: "2026-01-01T00:00:00Z",
              expires_at: "2099-01-01T00:00:00Z",
              existing_mapping_id: null,
              existing_mapping_version: null,
            },
            status,
            principal_type: "LOCAL_OPERATOR_SESSION",
          }),
          { status: 200 },
        );
      if (init?.method === "POST") throw new Error("unexpected post");
      return new Response(
        JSON.stringify({
          status: confirmed ? "ALREADY_MAPPED" : "ELIGIBLE_FOR_REVIEW",
          moysklad: { ...base, id: "m", account: "m1" },
          ozon: { ...base, id: "o", account: "o1" },
          mappings: [],
          history: confirmed
            ? [
                {
                  mapping_id: 7,
                  version: 1,
                  moysklad_warehouse_id: "m",
                  ozon_warehouse_id: "o",
                  provenance: "LOCAL_OPERATOR_SESSION",
                  confirmed_at: "2026-01-01T00:00:00Z",
                },
              ]
            : [],
          has_next: false,
          offset: 0,
        }),
        { status: 200 },
      );
    });
  render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <Harness />
    </QueryClientProvider>,
  );
  return mock;
}
async function create() {
  fireEvent.click(
    await screen.findByRole("button", { name: "Проверить пару складов" }),
  );
  await screen.findByText("Проверка складов готова к подтверждению");
}
it("requires explicit acknowledgement and retries the exact warehouse request after account switch", async () => {
  const mock = setup("CURRENT", true);
  await create();
  const button = screen.getByRole("button", {
    name: "Подтвердить соответствие складов",
  }) as HTMLButtonElement;
  expect(button.disabled).toBe(true);
  expect(
    mock.mock.calls.filter(([p]) => String(p).endsWith("/confirm")),
  ).toHaveLength(0);
  fireEvent.click(
    screen.getByLabelText(/Подтверждаю соответствие выбранных складов/),
  );
  fireEvent.click(button);
  fireEvent.click(button);
  await screen.findByText(/Результат подтверждения неизвестен/);
  fireEvent.click(screen.getByText("Switch accounts"));
  expect(screen.queryByText("Повторить то же подтверждение")).toBeNull();
  fireEvent.click(screen.getByText("К исходным аккаунтам"));
  fireEvent.click(screen.getByText("Повторить то же подтверждение"));
  await screen.findByText(/Ранее сохранённое подтверждение восстановлено/);
  const calls = mock.mock.calls.filter(([p]) => String(p).endsWith("/confirm"));
  expect(calls).toHaveLength(2);
  expect(calls[0][1]?.body).toBe(calls[1][1]?.body);
  expect(String(calls[0][0])).toContain("warehouse-mapping-reviews");
});
it("a session loss directs the operator to history and prevents replay from a new session", async () => {
  const mock = setup("CURRENT", false, "UNAUTHORIZED");
  await create();
  fireEvent.click(
    screen.getByLabelText(/Подтверждаю соответствие выбранных складов/),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Подтвердить соответствие складов" }),
  );
  await screen.findAllByText(/Сессия завершилась/);
  expect(screen.queryByText("Повторить то же подтверждение")).toBeNull();
  expect(
    mock.mock.calls.filter(([p]) => String(p).endsWith("/confirm")),
  ).toHaveLength(1);
});
it("a rejected stale review cannot be confirmed again", async () => {
  setup("CURRENT", false, "REVIEW_STALE");
  await create();
  fireEvent.click(
    screen.getByLabelText(/Подтверждаю соответствие выбранных складов/),
  );
  const button = screen.getByRole("button", {
    name: "Подтвердить соответствие складов",
  }) as HTMLButtonElement;
  fireEvent.click(button);
  await screen.findByText(/Подтверждение отклонено/);
  expect(button.disabled).toBe(true);
});
for (const status of ["EXPIRED", "STALE_SOURCE", "STALE_MAPPING"])
  it(`${status} review cannot start confirmation`, async () => {
    setup(status);
    await create();
    fireEvent.click(
      screen.getByLabelText(/Подтверждаю соответствие выбранных складов/),
    );
    expect(
      (
        screen.getByRole("button", {
          name: "Подтвердить соответствие складов",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
  });

it("does not trust an eligible status when the FBS classification is missing", async () => {
  vi.spyOn(globalThis, "fetch").mockResolvedValue(
    new Response(
      JSON.stringify({
        status: "ELIGIBLE_FOR_REVIEW",
        moysklad: { ...base, id: "m", account: "m1" },
        ozon: { ...base, id: "o", account: "o1", is_rfbs: null },
        mappings: [],
        history: [],
        has_next: false,
        offset: 0,
      }),
      { status: 200 },
    ),
  );
  render(
    <QueryClientProvider client={new QueryClient()}>
      <Harness />
    </QueryClientProvider>,
  );
  expect(
    (
      (await screen.findByRole("button", {
        name: "Проверить пару складов",
      })) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  expect(screen.getByText(/Тип склада:/).textContent).toContain("неизвестен");
});

it("blocks a hidden mapping conflict without revealing restricted history", async () => {
  vi.spyOn(globalThis, "fetch").mockResolvedValue(
    new Response(
      JSON.stringify({
        status: "MAPPING_CONFLICT",
        moysklad: { ...base, id: "m", account: "m1" },
        ozon: { ...base, id: "o", account: "o1" },
        mappings: [],
        history: [],
        has_next: false,
        offset: 0,
      }),
      { status: 200 },
    ),
  );
  render(
    <QueryClientProvider client={new QueryClient()}>
      <Harness />
    </QueryClientProvider>,
  );
  expect(
    await screen.findByText(/история за пределами доступных аккаунтов/),
  ).toBeTruthy();
  expect(
    (
      screen.getByRole("button", {
        name: "Проверить пару складов",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
});
