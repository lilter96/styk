import { useState } from "react";
import { afterEach, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ConfirmationNotice, useConfirmation } from "./Confirmation";
const original = { tenant: "t", moy: "m1", ozon: "o1" };
function Harness() {
  const [scope, setScope] = useState(original);
  const c = useConfirmation();
  return (
    <>
      <button
        onClick={() => c.start(scope, "p", "a".repeat(32), "same explanation")}
      >
        Start
      </button>
      <button onClick={() => setScope({ tenant: "t", moy: "m2", ozon: "o2" })}>
        Switch
      </button>
      <ConfirmationNotice confirmation={c} scope={scope} restore={setScope} />
    </>
  );
}
function mount() {
  return render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <Harness />
    </QueryClientProvider>,
  );
}
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});
it("retains the exact intent across account switch and uncertain outcome", async () => {
  let rejectRequest: (reason: Error) => void = () => {};
  const mock = vi
    .spyOn(globalThis, "fetch")
    .mockImplementationOnce(
      () =>
        new Promise((_resolve, reject) => {
          rejectRequest = reject;
        }),
    )
    .mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          mapping_id: 1,
          mapping_version: 1,
          review_id: "a".repeat(32),
          evidence_class: "OPERATOR_ATTESTED",
          confirmed_at: "2026-01-01T00:00:00Z",
          idempotent_replay: true,
        }),
        { status: 200 },
      ),
    );
  mount();
  fireEvent.click(screen.getByText("Start"));
  fireEvent.click(screen.getByText("Start"));
  fireEvent.click(screen.getByText("Switch"));
  expect(mock).toHaveBeenCalledTimes(1);
  rejectRequest(new Error("timeout"));
  await screen.findByText(/Результат подтверждения неизвестен/);
  expect(screen.queryByText("Повторить то же подтверждение")).toBeNull();
  fireEvent.click(screen.getByText("К исходным аккаунтам"));
  fireEvent.click(screen.getByText("Повторить то же подтверждение"));
  await screen.findByText(/Ранее сохранённое подтверждение восстановлено/);
  expect(mock.mock.calls[0][1]?.body).toEqual(mock.mock.calls[1][1]?.body);
});
it("session loss directs operator to history and does not retry", async () => {
  const mock = vi
    .spyOn(globalThis, "fetch")
    .mockResolvedValue(
      new Response(JSON.stringify({ error: "UNAUTHORIZED" }), { status: 401 }),
    );
  mount();
  fireEvent.click(screen.getByText("Start"));
  await waitFor(() =>
    expect(screen.getByText(/в новой сессии эта проверка не повторяется/)).toBeTruthy(),
  );
  expect(screen.queryByText("Повторить то же подтверждение")).toBeNull();
  expect(mock).toHaveBeenCalledTimes(1);
});
it("a rejected retry cannot erase a previously uncertain outcome", async () => {
  vi.spyOn(globalThis, "fetch")
    .mockRejectedValueOnce(new Error("timeout"))
    .mockResolvedValueOnce(
      new Response(JSON.stringify({ error: "REVIEW_EXPIRED" }), {
        status: 409,
      }),
    );
  mount();
  fireEvent.click(screen.getByText("Start"));
  await screen.findByText(/Результат подтверждения неизвестен/);
  fireEvent.click(screen.getByText("Повторить то же подтверждение"));
  await waitFor(() =>
    expect(screen.getByText("Повторить то же подтверждение")).toBeTruthy(),
  );
  expect(screen.getByText(/Результат подтверждения неизвестен/)).toBeTruthy();
});
for (const code of [
  "REVIEW_EXPIRED",
  "REVIEW_STALE",
  "MAPPING_CONFLICT",
  "INVALID_REQUEST",
  "FORBIDDEN",
]) {
  it(`reports ${code} without offering a new-key retry`, async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ error: code }), { status: 409 }),
    );
    mount();
    fireEvent.click(screen.getByText("Start"));
    await screen.findByText(new RegExp(code));
    expect(screen.queryByText("Повторить то же подтверждение")).toBeNull();
  });
}
it("keeps the intent after persistence failure", async () => {
  vi.spyOn(globalThis, "fetch").mockResolvedValue(
    new Response(JSON.stringify({ error: "PERSISTENCE_FAILURE" }), {
      status: 503,
    }),
  );
  mount();
  fireEvent.click(screen.getByText("Start"));
  await screen.findByText(/Результат подтверждения неизвестен/);
  expect(screen.getByText("Повторить то же подтверждение")).toBeTruthy();
});
