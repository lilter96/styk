import "@testing-library/jest-dom/vitest";
import { afterEach, expect, it } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { OrdersAddressMapping } from "./OrdersAddressMapping";
import type { OrderMapping } from "./OrdersTypes";

afterEach(cleanup);
it("keeps flat and structured address modes exclusive without removing contact mappings", () => {
  let latest: OrderMapping[] = [];
  function Harness() {
    const [value, setValue] = useState<OrderMapping[]>([
      { source: "customer.address", target: "shipment_address" },
      {
        source: "customer.phone",
        target: "attribute",
        attribute_id: "phone-native-id",
      },
    ]);
    return (
      <OrdersAddressMapping
        value={value}
        disabled={false}
        onChange={(next) => {
          latest = next;
          setValue(next);
        }}
      />
    );
  }
  render(<Harness />);
  fireEvent.click(screen.getByLabelText("Город → поле адреса отгрузки"));
  expect(
    screen.getByLabelText("Адрес доставки → нативное поле «Адрес отгрузки»"),
  ).not.toBeChecked();
  expect(latest).toContainEqual({
    source: "customer.address.city",
    target: "shipment_address_full.city",
  });
  fireEvent.click(screen.getByLabelText("Улица → поле адреса отгрузки"));
  expect(latest).toHaveLength(3);
  fireEvent.click(
    screen.getByLabelText("Адрес доставки → нативное поле «Адрес отгрузки»"),
  );
  expect(latest).toEqual([
    {
      source: "customer.phone",
      target: "attribute",
      attribute_id: "phone-native-id",
    },
    { source: "customer.address", target: "shipment_address" },
  ]);
});
it("does not select or invent country and region references", () => {
  render(
    <OrdersAddressMapping value={[]} disabled={false} onChange={() => {}} />,
  );
  expect(screen.getAllByRole("checkbox")).toHaveLength(6);
  expect(
    screen
      .getAllByRole("checkbox")
      .every((checkbox) => !(checkbox as HTMLInputElement).checked),
  ).toBe(true);
  expect(
    screen.getByText(
      /невыбранные поля существующего структурированного адреса сохраняются/,
    ),
  ).toBeVisible();
});
