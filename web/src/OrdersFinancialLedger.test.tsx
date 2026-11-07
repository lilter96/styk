import { afterEach, expect, it } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import {
  OrdersFinancialLedger,
  type FinancialLedger,
} from "./OrdersFinancialLedger";
import {
  emptyCommissionPolicy,
  financeConsentRequired,
} from "./OrdersFinanceTypes";
afterEach(cleanup);
const known = (value: string) => ({ presence: "value" as const, value });
export const ledgerFixture = (): FinancialLedger => ({
  version: "realization-v2-test",
  source_fingerprint: "a".repeat(64),
  period: { year: 2026, month: 10 },
  currency: "RUB",
  rows_total: 22,
  rows_classified: 22,
  rows_mapped_to_native: 20,
  rows_partial: 1,
  rows_unmapped: 1,
  components_total: 4,
  components_represented: 1,
  components_unrepresented: 3,
  unknown_components: 1,
  complete: false,
  rows: Array.from({ length: 22 }, (_, i) => ({
    source_row: i + 1,
    classification: i === 21 ? "UNKNOWN" : "SALE",
    native_status: i === 20 ? "PARTIAL" : i === 21 ? "UNMAPPED" : "MAPPED",
    blockers: [],
  })),
  components: [
    {
      id: "large",
      source_row: 1,
      side: "SALE",
      component: "amount",
      category: "SALE_GROSS",
      role: "BASE",
      value: known("9007199254740993.01"),
      currency: "RUB",
      sign: "POSITIVE",
      disposition: "NATIVE_SALE",
    },
    {
      id: "null",
      source_row: 21,
      side: "RETURN",
      component: "quantity",
      category: "QUANTITY",
      role: "QUANTITY",
      value: { presence: "null" },
      currency: "RUB",
      sign: "UNKNOWN",
      disposition: "UNREPRESENTED",
    },
    {
      id: "missing",
      source_row: 21,
      side: "RETURN",
      component: "total",
      category: "CONTROL_TOTAL",
      role: "CONTROL_TOTAL",
      value: { presence: "missing" },
      currency: "RUB",
      sign: "UNKNOWN",
      disposition: "UNREPRESENTED",
    },
    {
      id: "zero",
      source_row: 21,
      side: "SALE",
      component: "pick_up_point_coinvestment",
      category: "PICKUP_COINVESTMENT",
      role: "ADJUSTMENT",
      value: { presence: "zero", value: "0" },
      currency: "RUB",
      sign: "ZERO",
      disposition: "ZERO",
    },
  ],
  category_totals: [
    {
      category: "COMMISSION_FEE",
      side: "RETURN",
      role: "ADJUSTMENT",
      currency: "RUB",
      sum: "-0.01",
      known: 1,
      missing: 1,
      null: 1,
      positive: 0,
      negative: 1,
      zero: 0,
    },
  ],
  reconciliation: [
    {
      source_row: 21,
      side: "RETURN",
      gross: known("1.01"),
      reward: known("0.01"),
      expected_net: known("1"),
      source_net: known("0.99"),
      difference: known("0.01"),
      status: "DIFFERENCE",
    },
  ],
});
it("keeps missing NULL zero and large exact decimals separate and never calls a partial draft financially complete", () => {
  render(<OrdersFinancialLedger ledger={ledgerFixture()} />);
  expect(screen.getByText(/Финансовая сверка не завершена/)).toBeTruthy();
  fireEvent.click(screen.getByText("Построчная классификация и компоненты"));
  fireEvent.click(screen.getByText("Компоненты строки 1"));
  expect(screen.getByText(/9007199254740993.01/)).toBeTruthy();
  fireEvent.change(
    screen.getByLabelText("Показать строки финансового состава"),
    { target: { value: "review" } },
  );
  expect(screen.queryByText("Строка 1: Продажа")).toBeNull();
  fireEvent.click(screen.getByText("Компоненты строки 21"));
  expect(
    screen.getByText("quantity: NULL — значение неизвестно", {
      exact: true,
    }),
  ).toBeTruthy();
  expect(
    screen.getByText("total: Поле отсутствует RUB", { exact: true }),
  ).toBeTruthy();
  expect(screen.getByText("pick_up_point_coinvestment: 0 RUB")).toBeTruthy();
  fireEvent.click(screen.getByText("Итоги по компонентам и знак значений"));
  expect(screen.getByText("Корректировка: -0.01 RUB")).toBeTruthy();
  fireEvent.click(screen.getByText("Проверка сумм строк (1)"));
  expect(screen.getByText("Разница: 0.01 RUB.")).toBeTruthy();
});
it("paginates every row and keeps empty review filter recoverable", () => {
  const value = ledgerFixture();
  value.rows =
    value.rows?.map((row) => ({
      ...row,
      classification: "SALE",
      native_status: "MAPPED",
    })) ?? [];
  render(<OrdersFinancialLedger ledger={value} />);
  fireEvent.click(screen.getByText("Построчная классификация и компоненты"));
  expect(screen.queryByText("Строка 22: Продажа")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Следующие строки" }));
  expect(screen.getByText("Строка 22: Продажа")).toBeTruthy();
  fireEvent.change(
    screen.getByLabelText("Показать строки финансового состава"),
    { target: { value: "review" } },
  );
  expect(screen.getByText(/Строк по этому фильтру нет/)).toBeTruthy();
  fireEvent.change(
    screen.getByLabelText("Показать строки финансового состава"),
    { target: { value: "all" } },
  );
  expect(screen.getByText("Строка 1: Продажа")).toBeTruthy();
});
it("requires new owner consent to expand returns or partial accounting scope", () => {
  const p = { ...emptyCommissionPolicy(), enabled: true };
  expect(
    financeConsentRequired(p, { ...p, return_treatment: "COMMISSION_RETURN" }),
  ).toBe(true);
  expect(
    financeConsentRequired(p, { ...p, other_components: "PARTIAL_REVIEW" }),
  ).toBe(true);
  expect(financeConsentRequired(p, { ...p, enabled: false })).toBe(false);
});

it("keeps every residual component and reconciliation row reachable beyond the first page", () => {
  const value = ledgerFixture();
  const component = value.components![0];
  value.components = Array.from({ length: 31 }, (_, i) => ({
    ...component,
    id: `component-${i + 1}`,
    component: `component_${i + 1}`,
    disposition: "UNREPRESENTED",
  }));
  const check = value.reconciliation![0];
  value.reconciliation = Array.from({ length: 31 }, (_, i) => ({
    ...check,
    source_row: i + 1,
  }));
  render(<OrdersFinancialLedger ledger={value} />);
  fireEvent.click(screen.getByText("Неучтённые и неизвестные компоненты (31)"));
  const residual = within(
    screen
      .getByText("Неучтённые и неизвестные компоненты (31)")
      .closest("details")!,
  );
  expect(residual.queryByText(/component_31:/)).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Следующие компоненты" }));
  expect(residual.getByText(/component_31:/)).toBeTruthy();
  fireEvent.click(screen.getByText("Проверка сумм строк (31)"));
  expect(
    screen.queryByText("Строка 31 · Финансовый возврат: Есть расхождение"),
  ).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Следующие проверки" }));
  expect(
    screen.getByText("Строка 31 · Финансовый возврат: Есть расхождение"),
  ).toBeTruthy();
});

it("explains absent return fields and control totals separately from monetary planning", () => {
  const ledger = ledgerFixture();
  const sample = ledger.components![0];
  ledger.components = [
    ...Array.from({ length: 45 }, (_, i) => ({
      ...sample,
      id: `absent-${i}`,
      side: "RETURN",
      source_presence: "null",
      value: { presence: "missing" as const },
      disposition: "UNREPRESENTED",
    })),
    ...Array.from({ length: 5 }, (_, i) => ({
      ...sample,
      id: `control-${i}`,
      category: "CONTROL_TOTAL",
      source_presence: "value",
      disposition: "UNREPRESENTED",
    })),
    ...Array.from({ length: 19 }, (_, i) => ({
      ...sample,
      id: `money-${i}`,
      source_presence: "value",
      disposition: "UNREPRESENTED",
    })),
  ];
  render(<OrdersFinancialLedger ledger={ledger} />);
  const explanation = screen.getByRole("region", {
    name: "Что означает счётчик компонентов",
  });
  expect(explanation.textContent).toContain(
    "Поля отсутствующих блоков источника: 45",
  );
  expect(explanation.textContent).toContain("Контрольные суммы для сверки: 5");
  expect(explanation.textContent).toContain("Компоненты для проверки переноса: 19");
  expect(explanation.textContent).toContain("не нулевые суммы");
});
