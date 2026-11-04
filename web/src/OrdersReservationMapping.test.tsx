import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { OrdersReservationMapping } from "./OrdersReservationMapping";
import type { OrderMapping } from "./OrdersTypes";

afterEach(cleanup);

it("does not enable reserves or posting when opened", () => {
  let changed = false;
  render(
    <OrdersReservationMapping
      mappings={[]}
      disabled={false}
      onChange={() => {
        changed = true;
      }}
    />,
  );
  expect(screen.getByLabelText("Резерв в МойСклад")).toHaveValue("");
  expect(screen.getByLabelText("Проведение заказа МойСклад")).toHaveValue("");
  expect(changed).toBe(false);
});

it("keeps native posting and unrelated mappings separate from reserve authorization", () => {
  const mappings: OrderMapping[] = [
    { source: "posting_number", target: "name" },
    { source: "constant.zero", target: "positions.reserve" },
    { source: "constant.false", target: "applicable" },
  ];
  let latest: OrderMapping[] = [];
  render(
    <OrdersReservationMapping
      mappings={mappings}
      disabled={false}
      onChange={(next) => {
        latest = next;
      }}
    />,
  );
  fireEvent.change(screen.getByLabelText("Резерв в МойСклад"), {
    target: { value: "posting.open_quantity" },
  });
  expect(latest).toContainEqual({
    source: "posting.open_quantity",
    target: "positions.reserve",
  });
  expect(latest).toContainEqual(mappings[0]);
  expect(latest).toContainEqual(mappings[2]);
  expect(
    latest.filter((item) => item.target === "positions.reserve"),
  ).toHaveLength(1);
});

it("preserves a saved custom rule and allows explicitly withdrawing it", () => {
  let latest: OrderMapping[] = [];
  render(
    <OrdersReservationMapping
      mappings={[{ source: "custom.old_rule", target: "positions.reserve" }]}
      disabled={false}
      onChange={(next) => {
        latest = next;
      }}
    />,
  );
  expect(screen.getByLabelText("Резерв в МойСклад")).toHaveValue(
    "custom.old_rule",
  );
  fireEvent.change(screen.getByLabelText("Резерв в МойСклад"), {
    target: { value: "" },
  });
  expect(latest).toEqual([]);
});
