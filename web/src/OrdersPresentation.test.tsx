import { expect, it } from "vitest";
import { displayOrderMoney, targetPresentation } from "./OrdersPresentation";
import type { OrderTargetState } from "./OrdersTargetState";

it.each([
  ["0", "RUB", "0 ₽"],
  ["4290", "RUB", "4\u00a0290 ₽"],
  [
    "9007199254740993.01",
    "RUB",
    "9\u00a0007\u00a0199\u00a0254\u00a0740\u00a0993,01 ₽",
  ],
  ["-1000.50", "USD", "-1\u00a0000,50 USD"],
  ["0.001", "BYN", "0,001 BYN"],
])("formats exact money %s %s", (amount, currency, result) => {
  expect(displayOrderMoney({ amount, currency })).toBe(result);
});
it("does not infer a successful sync from absent or unknown state", () => {
  expect(targetPresentation().label).toBe("Неизвестно");
  const local = {
    has_operation: true,
    state: "ACKNOWLEDGED",
    unresolved: false,
    source_changed: false,
  } as OrderTargetState;
  expect(targetPresentation(local).label).toBe("Принято, ожидает сверки");
  expect(targetPresentation({ ...local, state: "UNKNOWN" }).tone).toBe(
    "warning",
  );
  expect(
    targetPresentation({ ...local, state: "RECONCILED", source_changed: true })
      .label,
  ).toBe("Нужна проверка");
});
