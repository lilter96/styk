import { afterEach, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { OrdersActionEligibility } from "./OrdersActionEligibility";
import type { OrderPosting } from "./OrdersTypes";

const posting = (extra: Partial<OrderPosting> = {}): OrderPosting => ({
  scope: {
    tenant: "synthetic",
    moysklad_account: "synthetic-moy",
    ozon_account: "synthetic-ozon",
  },
  commercial_order_id: "synthetic-order",
  posting_number: "synthetic-posting",
  parent_posting_number: { presence: "missing" },
  child_posting_numbers: [],
  order_number: { presence: "missing" },
  status: { presence: "value", value: "delivering" },
  substatus: { presence: "missing" },
  scheme: { presence: "value", value: "rFBS" },
  warehouse_id: { presence: "value", value: "synthetic-warehouse" },
  financial: { order_total: { presence: "missing" } },
  times: { created_at: { presence: "missing" } },
  lines: [],
  evidence: {
    revision: "synthetic-v1",
    provider_updated_at: { presence: "missing" },
    observed_at: new Date().toISOString(),
  },
  drift: [],
  ...extra,
});
afterEach(cleanup);
it("does not treat an observed status or reception time as action authority or a fresh check", () => {
  render(
    <OrdersActionEligibility posting={posting()} freshnessSeconds={300} />,
  );
  expect(screen.getByText("Нужна новая проверка источника")).toBeTruthy();
  expect(
    screen.getByText(/Статус отправления сам по себе не разрешает действие/),
  ).toBeTruthy();
  expect(screen.getByText(/Список доступных действий не получен/)).toBeTruthy();
});
it("preserves explicit false Express and distinguishes empty available actions from unknown", () => {
  render(
    <OrdersActionEligibility
      posting={posting({
        available_actions: [],
        evidence: {
          revision: "synthetic-v1",
          checked_at: new Date().toISOString(),
          observed_at: new Date().toISOString(),
          provider_updated_at: { presence: "missing" },
        },
        delivery_profile: {
          id: "profile-synthetic",
          revision: 1,
          warehouse_id: "synthetic-warehouse",
          delivery_method_id: "synthetic-method",
          scheme: "rFBS",
          carrier: { presence: "missing" },
          destination: { presence: "missing" },
          express: { presence: "zero", value: false },
          observed_at: new Date().toISOString(),
          valid_until: new Date(Date.now() + 60000).toISOString(),
          verified_dimensions: ["scheme", "express"],
        },
      })}
      freshnessSeconds={300}
    />,
  );
  expect(screen.getByText("Данные свежие")).toBeTruthy();
  expect(screen.getByText("Нет", { exact: true })).toBeTruthy();
  expect(screen.getByText(/Ozon не указал доступных действий/)).toBeTruthy();
  expect(screen.queryByText(/Список доступных действий не получен/)).toBeNull();
});

it("does not describe a future source check as fresh evidence", () => {
  render(
    <OrdersActionEligibility
      posting={posting({
        evidence: {
          revision: "synthetic-future",
          observed_at: new Date().toISOString(),
          provider_updated_at: { presence: "missing" },
          checked_at: new Date(Date.now() + 3600000).toISOString(),
        },
      })}
      freshnessSeconds={300}
    />,
  );
  expect(screen.getByText("Нужна новая проверка источника")).toBeTruthy();
});
