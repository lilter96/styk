import { afterEach, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { CatalogSyncOperations } from "./CatalogSyncOperations";
import { CatalogSyncPreview } from "./CatalogSyncPreview";
import {
  scopeBody,
  type CatalogWork,
  type Decision,
  type Identity,
  type Scope,
} from "./CatalogSyncTypes";

const scope: Scope = {
  tenant: "tenant",
  moy: "moy-account",
  ozon: "ozon-account",
};
const source: Identity = {
  account: { provider: "ozon", id: scope.ozon },
  id: "42",
  offer_id: "SOURCE-OFFER",
};
const target: Identity = {
  account: { provider: "moysklad", id: scope.moy },
  id: "",
  entity_type: "product",
};
const clients: QueryClient[] = [];

function blockedDecision(): Decision {
  return {
    enrollment_id: "creation",
    source: { identity: source, fields: {}, revision: "" },
    target: { identity: target, fields: {}, revision: "" },
    target_absent: false,
    ready: false,
    commands: [],
    validation: [{ code: "CREATED_TARGET_REQUIRES_MAPPING" }],
    plan: {
      mapping_id: "",
      mapping_version: 0,
      fingerprint: "",
      changes: [],
      conflicts: [],
      skipped: [],
      noop: false,
    },
  };
}

function creationWork(): CatalogWork {
  return {
    id: "creation-work",
    enrollment_id: "creation",
    state: "RECONCILED",
    decision: blockedDecision(),
    command: {
      action: "create",
      target,
      values: { name: { state: "known", data: "Approved name" } },
    },
    receipt: { state: "applied" },
  };
}

function mountHistory(work: CatalogWork) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity } },
  });
  clients.push(client);
  client.setQueryData(["catalog-operations", scope, ""], {
    operations: [
      {
        id: "operation",
        state: work.state,
        created_at: "2026-10-29T12:00:00Z",
        targets: [work],
      },
    ],
  });
  render(
    <QueryClientProvider client={client}>
      <CatalogSyncOperations scope={scope} />
    </QueryClientProvider>,
  );
}

afterEach(() => {
  cleanup();
  clients.forEach((client) => client.clear());
  clients.length = 0;
  vi.unstubAllGlobals();
});

it("shows the known source offer in a blocked creation preview without inventing a product name", () => {
  render(
    <CatalogSyncPreview
      preview={{
        id: "preview",
        scope: scopeBody(scope),
        config_revision: 1,
        expires_at: new Date(Date.now() + 60000).toISOString(),
        decisions: [blockedDecision()],
      }}
      busy={false}
      blocked={false}
      onApprove={() => {
        throw new Error("blocked decision cannot approve");
      }}
    />,
  );
  expect(screen.getByRole("heading", { name: "SOURCE-OFFER" })).toBeTruthy();
  expect(
    (
      screen.getByLabelText(
        "Подтвердить изменения товара SOURCE-OFFER",
      ) as HTMLInputElement
    ).disabled,
  ).toBe(true);
  expect(screen.queryByText("Товар без названия")).toBeNull();
});

it.each([
  [
    "verified created identity",
    "resolved-product-id",
    "receipt-product-id",
    "resolved-product-id",
  ],
  [
    "receipt identity before readback",
    undefined,
    "receipt-product-id",
    "receipt-product-id",
  ],
  ["pending provider identity", undefined, undefined, "Новый товар"],
])(
  "renders %s without changing the approved creation command",
  (_name, createdID, externalID, caption) => {
    const work = creationWork();
    if (createdID) work.created_target = { ...target, id: createdID };
    work.receipt.external_id = externalID;
    const approved = JSON.stringify(work.command);
    mountHistory(work);
    expect(
      screen.getByText(`МойСклад: ${caption}`, { exact: true }),
    ).toBeTruthy();
    expect(screen.getByRole("heading", { name: "SOURCE-OFFER" })).toBeTruthy();
    expect(JSON.stringify(work.command)).toBe(approved);
    expect(work.command.target.id).toBe("");
  },
);

it("preserves an existing approved target identity", () => {
  const work = creationWork();
  work.command = {
    ...work.command,
    action: "update",
    target: { ...target, id: "approved-target" },
  };
  work.created_target = { ...target, id: "different-created-id" };
  work.receipt.external_id = "different-receipt-id";
  mountHistory(work);
  expect(
    screen.getByText("МойСклад: approved-target", { exact: true }),
  ).toBeTruthy();
  expect(
    screen.queryByText(/different-created-id|different-receipt-id/),
  ).toBeNull();
});

it("keeps the Ozon offer namespace alongside its newly created provider product ID", () => {
  const work = creationWork();
  work.command = {
    ...work.command,
    target: { account: source.account, id: "", offer_id: "SELLER-OFFER" },
  };
  work.created_target = { ...work.command.target, id: "provider-product-id" };
  mountHistory(work);
  expect(
    screen.getByText("Ozon: SELLER-OFFER · ID provider-product-id", {
      exact: true,
    }),
  ).toBeTruthy();
});

function unknownWork(): CatalogWork {
  return {
    ...creationWork(),
    id: "unknown-ozon-work",
    state: "UNKNOWN",
    code: "PROVIDER_OUTCOME_UNKNOWN",
    command: {
      action: "update",
      target: source,
      values: { name: { state: "known", data: "Reviewed name" } },
    },
    receipt: {
      state: "partial",
      task_id: "known-task",
      errors: [
        {
          field: "attributes",
          code: "OZON_WARNING_CODE",
          severity: "warning",
          attribute_id: 85,
        },
      ],
    },
  };
}

const reconciliationButton = () =>
  screen.getByRole("button", {
    name: "Сверить результат без повторной отправки",
  }) as HTMLButtonElement;
const acknowledgeReconciliation = () =>
  fireEvent.click(
    screen.getByRole("checkbox", { name: /Я принимаю предупреждения Ozon/ }),
  );
function openReconciliation() {
  fireEvent.click(
    screen.getByText("Сверить результат без повторной отправки", {
      selector: "summary",
    }),
  );
}

it("requires explicit acknowledgement and a reason before one bounded read-only reconciliation request, then refreshes history", async () => {
  const work = unknownWork();
  const refreshed = {
    ...work,
    state: "RECONCILED",
    code: "READBACK_MATCHED_WITH_WARNINGS",
  };
  const fetchMock = vi.fn(
    async (input: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === "POST")
        return new Response(
          JSON.stringify({
            id: "audit-id",
            work_id: work.id,
            state: "RECONCILED",
            code: "READBACK_MATCHED_WITH_WARNINGS",
            completed: true,
          }),
          { status: 200 },
        );
      expect(String(input)).toContain("/api/catalog-sync/operations?");
      return new Response(
        JSON.stringify({
          operations: [
            {
              id: "operation",
              state: "RECONCILED",
              created_at: "2026-10-29T12:00:00Z",
              targets: [refreshed],
            },
          ],
        }),
        { status: 200 },
      );
    },
  );
  vi.stubGlobal("fetch", fetchMock);
  mountHistory(work);
  openReconciliation();
  expect(reconciliationButton().disabled).toBe(true);
  fireEvent.click(reconciliationButton());
  expect(fetchMock).not.toHaveBeenCalled();
  expect(
    screen.getByText(/может разрешить выполнение остальных изменений/),
  ).toBeTruthy();
  acknowledgeReconciliation();
  const reason = screen.getByLabelText("Причина сверки");
  fireEvent.change(reason, { target: { value: " " } });
  expect(reconciliationButton().disabled).toBe(true);
  fireEvent.change(reason, { target: { value: " Проверка вручную " } });
  expect(reconciliationButton().disabled).toBe(false);
  const button = reconciliationButton();
  fireEvent.click(button);
  fireEvent.click(button);
  await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
  const writes = fetchMock.mock.calls.filter(
    ([, init]) => init?.method === "POST",
  );
  expect(writes).toHaveLength(1);
  expect(String(writes[0][0])).toBe("/api/catalog-sync/reconcile");
  expect(JSON.parse(String(writes[0][1]?.body))).toEqual({
    ...scopeBody(scope),
    work_id: work.id,
    idempotency_key: expect.any(String),
    acknowledge: true,
    accept_warnings: true,
    reason: "Проверка вручную",
  });
  expect(writes[0][1]?.signal).toBeInstanceOf(AbortSignal);
  expect((writes[0][1]?.signal as AbortSignal).aborted).toBe(false);
  await waitFor(() =>
    expect(
      screen.getByText(
        "Выбранные поля подтверждены; предупреждения Ozon сохранены",
        { selector: "span" },
      ),
    ).toBeTruthy(),
  );
  expect(
    screen.getByText(/Первоначальная отправка имела неизвестный результат/),
  ).toBeTruthy();
  expect(
    screen.getByText("OZON_WARNING_CODE", { selector: "code" }),
  ).toBeTruthy();
  expect(work.state).toBe("UNKNOWN");
});

it("never retries a reconciliation with a lost response and permits only safe history refresh", async () => {
  const work = unknownWork();
  const fetchMock = vi.fn(
    async (input: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === "POST")
        throw new TypeError("Lost response with unsafe provider detail");
      expect(String(input)).toContain("/api/catalog-sync/operations?");
      return new Response(
        JSON.stringify({
          operations: [
            {
              id: "operation",
              state: "UNKNOWN",
              created_at: "2026-10-29T12:00:00Z",
              targets: [work],
            },
          ],
        }),
        { status: 200 },
      );
    },
  );
  vi.stubGlobal("fetch", fetchMock);
  mountHistory(work);
  openReconciliation();
  acknowledgeReconciliation();
  fireEvent.click(reconciliationButton());
  await waitFor(() =>
    expect(screen.getByText(/Результат сверки неизвестен/)).toBeTruthy(),
  );
  expect(reconciliationButton().disabled).toBe(true);
  fireEvent.click(reconciliationButton());
  fireEvent.click(screen.getByRole("button", { name: "Обновить статусы" }));
  await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
  expect(
    fetchMock.mock.calls.filter(([, init]) => init?.method === "POST"),
  ).toHaveLength(1);
  expect(reconciliationButton().disabled).toBe(true);
  expect(screen.getAllByText("Результат неизвестен")).toHaveLength(2);
  expect(screen.queryByText(/unsafe provider detail/)).toBeNull();
});

it("does not treat an HTTP acknowledgement or an incomplete result as reconciled", async () => {
  const work = unknownWork();
  const fetchMock = vi.fn(
    async (_input: RequestInfo | URL, init?: RequestInit) =>
      new Response(
        JSON.stringify(
          init?.method === "POST"
            ? {
                id: "audit-id",
                work_id: work.id,
                state: "RECONCILED",
                code: "READBACK_MATCHED_WITH_WARNINGS",
                completed: false,
              }
            : {
                operations: [
                  {
                    id: "operation",
                    state: "UNKNOWN",
                    created_at: "2026-10-29T12:00:00Z",
                    targets: [work],
                  },
                ],
              },
        ),
        { status: 200 },
      ),
  );
  vi.stubGlobal("fetch", fetchMock);
  mountHistory(work);
  openReconciliation();
  acknowledgeReconciliation();
  fireEvent.click(reconciliationButton());
  await waitFor(() =>
    expect(screen.getByText(/Сверка не подтвердила результат/)).toBeTruthy(),
  );
  expect(
    screen.queryByText(
      "Выбранные поля подтверждены; предупреждения Ozon сохранены",
    ),
  ).toBeNull();
  expect(reconciliationButton().disabled).toBe(true);
});

it("renders retained warning severity without red error styling or unsafe provider messages", () => {
  const work = unknownWork();
  work.receipt.errors![0].message = "Unsafe raw provider message";
  work.receipt.errors!.push({
    field: "name",
    code: "UNRECOGNIZED_ERROR",
    severity: "error",
    message: "Another unsafe provider message",
  });
  mountHistory(work);
  const code = screen.getByText("OZON_WARNING_CODE", { selector: "code" });
  expect(code.closest(".catalog-warning")).toBeTruthy();
  expect(code.closest(".catalog-error")).toBeNull();
  expect(
    screen
      .getByText("UNRECOGNIZED_ERROR", { selector: "code" })
      .closest(".catalog-error"),
  ).toBeTruthy();
  expect(screen.queryByText(/unsafe.*provider message/i)).toBeNull();
  expect(screen.getByText("ID характеристики: 85")).toBeTruthy();
});

it.each(["missing task", "non-Ozon", "already reconciled"])(
  "does not offer reconciliation for %s",
  (kind) => {
    const work = unknownWork();
    if (kind === "missing task") delete work.receipt.task_id;
    if (kind === "non-Ozon") work.command.target = target;
    if (kind === "already reconciled") work.state = "RECONCILED";
    mountHistory(work);
    expect(
      screen.queryByText("Сверить результат без повторной отправки"),
    ).toBeNull();
  },
);

it("shows each provider diagnostic once when repeated at task and product level", () => {
  const work = unknownWork();
  const original = work.receipt.errors![0];
  work.receipt.products = [
    {
      status: "imported",
      errors: [{ ...original }, { ...original, attribute_id: 86 }],
    },
  ];
  const recorded = JSON.stringify(work.receipt);
  mountHistory(work);
  expect(
    screen.getAllByText("OZON_WARNING_CODE", { selector: "code" }),
  ).toHaveLength(2);
  expect(screen.getAllByText("ID характеристики: 85")).toHaveLength(1);
  expect(screen.getAllByText("ID характеристики: 86")).toHaveLength(1);
  expect(JSON.stringify(work.receipt)).toBe(recorded);
});
