import { afterEach, expect, it } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { Overview } from "./App";
import type { Report } from "./api";

afterEach(cleanup);

function report(): Report {
  return {
    checks: [
      { code: "MOYSKLAD_IMPORT", status: "PASS", detail: "" },
      { code: "OZON_IMPORT", status: "FAIL", detail: "" },
      { code: "WAREHOUSE_MAPPINGS", status: "WARN", detail: "" },
    ],
    batches: {
      moysklad: {
        present: true,
        completed: true,
        age: "1m",
        counts: { products: 37, warehouses: 2, stock_rows: 81 },
      },
      ozon: {
        present: true,
        completed: false,
        age: "1m",
        counts: { products: 12, warehouses: 0, stock_rows: 19 },
      },
    },
    mapping_counts: {
      catalog_products: 12,
      catalog_confirmed_products: 3,
      confirmed_warehouses: 2,
    },
    decision_status: "NOT_CHECKED",
    decision_reasons: [],
    publication: "DISABLED",
  };
}
const view = (data: Report) => (
  <MemoryRouter>
    <Overview report={data} loading={false} error={null} />
  </MemoryRouter>
);

it("uses current report counts and switches chart metrics without replacing missing values with zero", () => {
  const data = report();
  delete data.batches.moysklad.counts!.stock_rows;
  render(view(data));
  expect(screen.getByRole("img", { name: "МойСклад: 37" })).toBeTruthy();
  expect(screen.getByRole("img", { name: "Ozon: 12" })).toBeTruthy();
  expect(
    screen.getByRole("img", { name: "Подтверждено 3 из 12 товаров" }),
  ).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Склады" }));
  expect(screen.getByRole("img", { name: "Ozon: 0" })).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Записи остатков" }));
  expect(
    screen.getByRole("img", { name: "МойСклад: Нет данных" }),
  ).toBeTruthy();
  expect(screen.getByRole("img", { name: "Ozon: 19" })).toBeTruthy();
});

it("replaces every dashboard value when the selected account report changes", () => {
  const mounted = render(view(report()));
  const next = report();
  next.batches.ozon.counts!.products = 8;
  next.mapping_counts = {
    catalog_products: 8,
    catalog_confirmed_products: 8,
    confirmed_warehouses: 4,
  };
  next.checks = [{ code: "WAREHOUSE_MAPPINGS", status: "PASS", detail: "" }];
  mounted.rerender(view(next));
  expect(screen.getByRole("img", { name: "Ozon: 8" })).toBeTruthy();
  expect(screen.queryByRole("img", { name: "Ozon: 12" })).toBeNull();
  expect(
    screen.getByRole("img", { name: "Подтверждено 8 из 8 товаров" }),
  ).toBeTruthy();
  expect(screen.getByText("100%")).toBeTruthy();
  expect(screen.getByText("Все выполненные проверки пройдены.")).toBeTruthy();
  const warehouseMetric = screen
    .getByRole("heading", { name: "Связи складов" })
    .closest("article")!;
  expect(within(warehouseMetric).getByText("4")).toBeTruthy();
});

it("shows unavailable catalog coverage honestly and routes real issues to their working sections", () => {
  const data = report();
  data.batches.ozon = {
    present: false,
    completed: false,
    age: "",
    counts: { products: 999 },
  };
  data.mapping_counts = { confirmed_products: 999 };
  render(view(data));
  expect(screen.getByRole("img", { name: "Ozon: Нет данных" })).toBeTruthy();
  expect(
    screen.getByRole("img", {
      name: "Нет данных о сопоставлении текущего каталога",
    }),
  ).toBeTruthy();
  expect(screen.queryByText("999")).toBeNull();
  const attention = screen.getByRole("region", { name: "Требуют внимания" });
  expect(
    within(attention)
      .getByRole("link", { name: /Загрузка Ozon/ })
      .getAttribute("href"),
  ).toBe("/integrations");
  expect(
    within(attention)
      .getByRole("link", { name: /Соответствия складов/ })
      .getAttribute("href"),
  ).toBe("/warehouses");
  expect(within(attention).queryByText("Загрузка МойСклад")).toBeNull();
});
