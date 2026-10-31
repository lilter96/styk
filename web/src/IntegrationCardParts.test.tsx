import { afterEach, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { IntegrationMetrics } from "./IntegrationCardParts";
afterEach(cleanup);
it("preserves server totals and distinguishes zero from missing observations", () => {
  render(
    <IntegrationMetrics
      provider="moysklad"
      batch={{
        present: true,
        completed: true,
        age: "1m",
        counts: { products: 1400, variants: 0 },
      }}
    />,
  );
  expect(screen.getByText("1 400", { exact: false })).toBeTruthy();
  expect(screen.getByText("0", { exact: true })).toBeTruthy();
  expect(screen.getByText("—", { exact: true })).toBeTruthy();
});
it("does not advertise an incomplete import as updated metrics", () => {
  render(
    <IntegrationMetrics
      provider="ozon"
      batch={{
        present: true,
        completed: false,
        age: "1m",
        counts: { products: 99 },
      }}
    />,
  );
  expect(screen.queryByText("99")).toBeNull();
  expect(screen.queryByText("Вариантов")).toBeNull();
  expect(screen.getByText("Нет данных")).toBeTruthy();
});
