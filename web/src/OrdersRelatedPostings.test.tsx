import { afterEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { OrdersRelatedPostings } from "./OrdersRelatedPostings";
import type { RelatedPostingGroup } from "./OrdersTypes";

afterEach(cleanup);

it("keeps related membership distinct from parent and child links", () => {
  const find = vi.fn();
  render(
    <OrdersRelatedPostings
      current="current"
      onFind={find}
      group={{
        presence: "value",
        posting_numbers: {
          presence: "value",
          value: ["current", "9007199254740993123-1"],
        },
      }}
    />,
  );
  expect(screen.queryByText(/Родительское|Дочернее/)).toBeNull();
  expect(screen.queryByRole("button", { name: /current/ })).toBeNull();
  fireEvent.click(
    screen.getByRole("button", {
      name: "Найти связанное отправление 9007199254740993123-1",
    }),
  );
  expect(find).toHaveBeenCalledExactlyOnceWith("9007199254740993123-1");
  expect(
    screen.getByText("Связь сборки не означает, что резерв уже перенесён."),
  ).toBeTruthy();
});

it.each([
  { presence: "null", posting_numbers: { presence: "missing" } },
  { presence: "empty", posting_numbers: { presence: "missing" } },
  { presence: "value", posting_numbers: { presence: "null" } },
] satisfies RelatedPostingGroup[])(
  "does not call incomplete membership an empty list: %s",
  (group) => {
    render(
      <OrdersRelatedPostings
        current="current"
        onFind={vi.fn()}
        group={group}
      />,
    );
    expect(
      screen.getByText("Ozon не передал список связанных отправлений."),
    ).toBeTruthy();
    expect(screen.queryByText("Ozon передал пустой список связей.")).toBeNull();
  },
);

it("represents an explicit empty list separately", () => {
  render(
    <OrdersRelatedPostings
      current="current"
      onFind={vi.fn()}
      group={{ presence: "value", posting_numbers: { presence: "empty" } }}
    />,
  );
  expect(screen.getByText("Ozon передал пустой список связей.")).toBeTruthy();
});

it("does not invent a group when the source omitted it", () => {
  const { container } = render(
    <OrdersRelatedPostings current="current" onFind={vi.fn()} />,
  );
  expect(container.textContent).toBe("");
});

it("bounds initial large assembly rendering and supports explicit expansion", () => {
  const numbers = Array.from({ length: 30 }, (_, i) => `posting-${i}`);
  render(
    <OrdersRelatedPostings
      current="current"
      onFind={vi.fn()}
      group={{
        presence: "value",
        posting_numbers: { presence: "value", value: numbers },
      }}
    />,
  );
  expect(
    screen.getAllByRole("button", { name: /Найти связанное/ }),
  ).toHaveLength(10);
  fireEvent.click(screen.getByRole("button", { name: "Показать ещё 20" }));
  expect(
    screen.getAllByRole("button", { name: /Найти связанное/ }),
  ).toHaveLength(30);
  expect(
    screen
      .getByRole("button", { name: "Свернуть список связей" })
      .getAttribute("aria-expanded"),
  ).toBe("true");
});
