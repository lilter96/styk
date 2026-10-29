import { afterEach, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
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
  expect(
    screen.getByRole("heading", { name: "SOURCE-OFFER" }),
  ).toBeTruthy();
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
    expect(
      screen.getByRole("heading", { name: "SOURCE-OFFER" }),
    ).toBeTruthy();
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
