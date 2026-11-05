import "@testing-library/jest-dom/vitest";
import { afterEach, expect, it, vi } from "vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { OrdersExemplars, validExemplarSummary } from "./OrdersExemplars";
import { exemplarInput, OrdersExemplarEditor } from "./OrdersExemplarEditor";
import { OrdersActionSummary } from "./OrdersActionSummary";
import type { ExemplarPublic } from "./OrdersExemplarTypes";
import { orderScopeBody, type OrderCommand } from "./OrdersTypes";

const scope = { tenant: "synthetic", moy: "moy", ozon: "ozon" };
const data = (): ExemplarPublic => ({
  scope: { tenant: "synthetic", moysklad_account: "moy", ozon_account: "ozon" },
  work_id: "prepare-1",
  proof_work_id: "proof-1",
  source_revision: "rev1",
  target_fingerprint: "fp1",
  posting_number: "P-1",
  proof_valid_until: new Date(Date.now() + 1000).toISOString(),
  expires_at: new Date(Date.now() + 86400000).toISOString(),
  products: [
    {
      sku: "9007199254740993",
      quantity: 1,
      ids: ["9007199254740995"],
      required: {
        imei: { presence: "zero", value: false },
        mandatory_mark: { presence: "value", value: true },
      },
    },
  ],
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});
it("validates exact account, posting and distinct provider identifiers without rounding", () => {
  expect(validExemplarSummary(data(), scope, "P-1")).toBe(true);
  expect(
    validExemplarSummary(data(), { ...scope, tenant: "other" }, "P-1"),
  ).toBe(false);
  expect(validExemplarSummary(data(), scope, "OTHER")).toBe(false);
  const copy = data();
  copy.products[0].quantity = 2;
  copy.products[0].ids.push(copy.products[0].ids[0]);
  expect(validExemplarSummary(copy, scope, "P-1")).toBe(false);
});
it("preserves untouched fields, scanner separators, and intentional clear separately", () => {
  const key = JSON.stringify([
    data().products[0].sku,
    data().products[0].ids[0],
  ]);
  expect(exemplarInput(data(), {})).toBeUndefined();
  const input = exemplarInput(data(), {
    [key]: {
      mandatory_mark: "01SCAN\u001d21ACTUAL",
      imei: "old",
      imei_clear: "yes",
      gtd_mode: "absent",
    },
  });
  expect(input?.products[0].exemplars[0]).toEqual({
    id: "9007199254740995",
    gtd_absent: { presence: "value", value: true },
    marks: {
      presence: "value",
      value: [{ type: "mandatory_mark", value: "01SCAN\u001d21ACTUAL" }],
    },
    clear_mark_types: ["imei"],
  });
  expect(
    exemplarInput(data(), { [key]: { gtd_mode: "value", imei: "actual" } }),
  ).toBeUndefined();
});
it("bounds the editor to ten units and retains edits between pages", () => {
  const copy = data();
  copy.products[0].quantity = 11;
  copy.products[0].ids = Array.from({ length: 11 }, (_, index) =>
    String(index + 1),
  );
  const change = vi.fn();
  render(
    <OrdersExemplarEditor data={copy} disabled={false} onChange={change} />,
  );
  expect(screen.getAllByLabelText("Код маркировки")).toHaveLength(10);
  fireEvent.change(screen.getAllByLabelText("Код маркировки")[0], {
    target: { value: "actual-mark" },
  });
  fireEvent.click(screen.getByText("Следующие экземпляры"));
  expect(screen.getAllByLabelText("Код маркировки")).toHaveLength(1);
  fireEvent.click(screen.getByText("Предыдущие экземпляры"));
  expect(screen.getAllByLabelText("Код маркировки")[0]).toHaveValue(
    "actual-mark",
  );
});
it("expires five-minute packing proof without polling or waiting for 24-hour secret expiry", async () => {
  const snapshot = data();
  const fetch = vi.fn(
    async () =>
      new Response(JSON.stringify(snapshot), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
  );
  vi.stubGlobal("fetch", fetch);
  const changed = vi.fn();
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <OrdersExemplars
        scope={scope}
        postingNumber="P-1"
        sourceRevision="rev1"
        mode="proof"
        disabled={false}
        onChange={changed}
      />
    </QueryClientProvider>,
  );
  const checkbox = await screen.findByRole("checkbox");
  fireEvent.click(checkbox);
  expect(changed).toHaveBeenLastCalledWith({
    workID: "proof-1",
    validUntil: snapshot.proof_valid_until,
  });
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 1100));
  });
  expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
  expect(changed).toHaveBeenLastCalledWith(undefined);
  expect(fetch).toHaveBeenCalledTimes(1);
});
it("never displays private codes from a command in preview summary", () => {
  const command = {
    scope: orderScopeBody(scope),
    action: "posting_exemplar_set",
    posting_action: {
      parameters: {
        exemplar_work_id: "prepare-1",
        exemplars: {
          products: [
            {
              sku: "123",
              exemplars: [
                {
                  id: "456",
                  gtd: { presence: "value", value: "SENSITIVE-GTD" },
                  marks: {
                    presence: "value",
                    value: [{ type: "imei", value: "SENSITIVE-IMEI" }],
                  },
                },
              ],
            },
          ],
        },
      },
    },
  } as OrderCommand;
  render(<OrdersActionSummary command={command} />);
  expect(screen.getByText(/сведения экземпляров введены/i)).toBeInTheDocument();
  expect(document.body).not.toHaveTextContent("SENSITIVE-");
});
