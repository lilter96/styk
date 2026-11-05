import "@testing-library/jest-dom/vitest";
import { afterEach, expect, it, vi } from "vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { OrdersRecovery } from "./OrdersRecovery";
import { orderScopeBody, type OrderWork } from "./OrdersTypes";

const scope = { tenant: "synthetic", moy: "moy", ozon: "ozon" };
const work = {
  id: "old-work",
  state: "UNKNOWN",
  automatic: false,
  scope: orderScopeBody(scope),
  command: {
    scope: orderScopeBody(scope),
    id: "command",
    action: "mirror_update",
    posting_number: "POSTING",
    correlation: { customer_order_id: "native" },
  },
  receipt: { state: "UNKNOWN", code: "TRANSPORT_ERROR" },
} as OrderWork;
const evidence = () => ({
  id: "evidence-1",
  scope: orderScopeBody(scope),
  work_id: work.id,
  created_at: new Date().toISOString(),
  expires_at: new Date(Date.now() + 60000).toISOString(),
  source: {
    observed_at: new Date().toISOString(),
    status: { presence: "value", value: "awaiting_packaging" },
  },
  target: {
    observed_at: new Date().toISOString(),
    complete: true,
    positions: [],
  },
});
function mount(fetcher: typeof fetch, onAuthorization = vi.fn()) {
  vi.stubGlobal("fetch", fetcher);
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  render(
    <QueryClientProvider client={client}>
      <OrdersRecovery
        scope={scope}
        work={work}
        onAuthorization={onAuthorization}
      />
    </QueryClientProvider>,
  );
  const details = screen
    .getByText("Проверить неизвестный результат и варианты восстановления")
    .closest("details")!;
  details.open = true;
  fireEvent(details, new Event("toggle"));
  return onAuthorization;
}
const response = (value: unknown) =>
  new Response(JSON.stringify(value), {
    headers: { "Content-Type": "application/json" },
  });
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});
it("opening recovery only reads and requires check plus explicit uncertainty acknowledgment", async () => {
  const calls: string[] = [];
  let approved: Record<string, unknown> | undefined;
  const authorization = mount(
    vi.fn(async (url, init) => {
      calls.push(`${init?.method ?? "GET"} ${url}`);
      if (String(url).endsWith("/check"))
        return response({
          work: { ...work, scope: orderScopeBody(scope) },
          eligible: true,
          blockers: [],
          evidence: evidence(),
        });
      if (String(url).endsWith("/resolve")) {
        approved = JSON.parse(String(init?.body));
        return response({
          work: { ...work, scope: orderScopeBody(scope) },
          eligible: false,
          blockers: [],
          evidence: evidence(),
          resolution: {
            id: "resolution",
            accepted_uncertainty: true,
            created_at: new Date().toISOString(),
            attestation: {
              id: "attestation",
              valid_until: new Date(Date.now() + 60000).toISOString(),
            },
          },
        });
      }
      return response({
        work: { ...work, scope: orderScopeBody(scope) },
        eligible: false,
        blockers: [{ code: "RECOVERY_CHECK_REQUIRED" }],
      });
    }),
  );
  await screen.findByText(
    "Запросите свежую сверку источника и заказа МойСклад.",
  );
  expect(calls).toHaveLength(1);
  expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
  fireEvent.click(screen.getByText("Сверить результат чтением"));
  const reason = await screen.findByRole("textbox");
  const button = screen.getByText("Записать разрешение нового решения");
  expect(button).toBeDisabled();
  fireEvent.change(reason, {
    target: { value: "Проверено чтением, принимаю задержанное применение" },
  });
  fireEvent.click(
    screen.getByLabelText(
      "Прежний обработчик остановлен; активная отправка этого заказа исключена.",
    ),
  );
  expect(button).toBeDisabled();
  fireEvent.click(
    screen.getByLabelText(
      "Принимаю риск позднего применения прежнего запроса. Разрешаю только новый предпросмотр и отдельное подтверждение.",
    ),
  );
  expect(button).toBeEnabled();
  fireEvent.click(button);
  await screen.findByText(/Принятие неопределённости записано/);
  expect(approved).toMatchObject({
    tenant: scope.tenant,
    work_id: work.id,
    evidence_id: "evidence-1",
    acknowledge: true,
    worker_stopped: true,
  });
  expect(approved?.idempotency_key).toBeTypeOf("string");
  expect(calls.filter((call) => call.startsWith("POST"))).toHaveLength(2);
  await waitFor(() =>
    expect(authorization).toHaveBeenCalledWith("POSTING", expect.any(String)),
  );
  expect(localStorage.length).toBe(0);
});
it("does not show another account's recovery evidence", async () => {
  mount(
    vi.fn(async () =>
      response({
        work: { ...work, scope: { ...orderScopeBody(scope), tenant: "other" } },
        eligible: true,
        evidence: evidence(),
        blockers: [],
      }),
    ),
  );
  await screen.findByRole("alert");
  expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
});
it("revokes expired recovery authorization without another network mutation", async () => {
  const until = new Date(Date.now() + 700).toISOString();
  const fetcher = vi.fn(async () =>
    response({
      work: { ...work, scope: orderScopeBody(scope) },
      eligible: false,
      blockers: [],
      resolution: {
        id: "resolution",
        created_at: new Date().toISOString(),
        accepted_uncertainty: true,
        attestation: { id: "attestation", valid_until: until },
      },
    }),
  );
  const authorization = mount(fetcher);
  await screen.findByText(/Новое решение можно проверить до/);
  expect(authorization).toHaveBeenLastCalledWith("POSTING", until);
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 800));
  });
  expect(authorization).toHaveBeenLastCalledWith("POSTING", undefined);
  expect(screen.getByText(/Срок разрешения истёк/)).toBeVisible();
  expect(fetcher).toHaveBeenCalledTimes(1);
});
