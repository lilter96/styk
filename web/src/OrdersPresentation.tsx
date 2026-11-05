import { Icon } from "./UI";
import type { OrderTargetState } from "./OrdersTargetState";
import {
  knownOrderField,
  orderFieldText,
  orderStateLabels,
  postingStatusLabels,
  type OrderPosting,
  type OrderMoney,
} from "./OrdersTypes";

export function orderQuantity(lines: OrderPosting["lines"]): string {
  if (
    !lines.length ||
    lines.some((line) => !/^\d+(\.\d+)?$/.test(line.quantity))
  )
    return "Неизвестно";
  const scale = Math.max(
    ...lines.map((line) => line.quantity.split(".")[1]?.length ?? 0),
  );
  const sum = lines.reduce((total, line) => {
    const [whole, fraction = ""] = line.quantity.split(".");
    return total + BigInt(whole + fraction.padEnd(scale, "0"));
  }, 0n);
  if (!scale) return String(sum);
  const digits = String(sum).padStart(scale + 1, "0");
  return `${digits.slice(0, -scale)}.${digits.slice(-scale)}`.replace(
    /\.?0+$/,
    "",
  );
}

// Keep decimal money and identifiers as strings, including values beyond 2^53.
export function displayOrderMoney(value: OrderMoney): string {
  if (!/^-?\d+(\.\d+)?$/.test(value.amount))
    return `${value.amount} ${value.currency}`;
  const [whole, fraction] = value.amount.split(".");
  const amount =
    whole.replace(/\B(?=(\d{3})+(?!\d))/g, "\u00a0") +
    (fraction ? `,${fraction}` : "");
  return `${amount} ${value.currency === "RUB" ? "₽" : value.currency}`;
}
export function postingTotal(posting: OrderPosting): string {
  const money = knownOrderField(posting.financial?.order_total);
  if (money !== undefined) return displayOrderMoney(money);
  const amount = knownOrderField(posting.financial?.order_total_amount);
  const currency = knownOrderField(posting.financial?.currency);
  if (amount !== undefined)
    return currency
      ? displayOrderMoney({ amount, currency })
      : `${amount} (валюта неизвестна)`;
  return orderFieldText(posting.financial?.order_total);
}
export function PostingStatus({ posting }: { posting: OrderPosting }) {
  const status = knownOrderField(posting.status);
  return (
    <span className="orders-posting-status" data-status={status}>
      {status === "delivered" && <Icon name="check" size="button" />}
      {status === "cancelled" && <Icon name="close" size="button" />}
      {status
        ? (postingStatusLabels[status] ?? status)
        : orderFieldText(posting.status)}
    </span>
  );
}
export function targetPresentation(local?: OrderTargetState, locked = false) {
  if (locked || local?.unresolved)
    return {
      label: "Результат неизвестен",
      tone: "warning",
      icon: "error" as const,
    };
  if (!local)
    return { label: "Неизвестно", tone: "muted", icon: "more" as const };
  if (local.source_changed)
    return { label: "Нужна проверка", tone: "warning", icon: "error" as const };
  if (!local.has_operation)
    return {
      label: local.customer_order_id ? "Заказ связан" : "Не передан",
      tone: "muted",
      icon: "orders" as const,
    };
  if (local.state === "RECONCILED")
    return {
      label: "Подтверждено сверкой",
      tone: "success",
      icon: "check" as const,
    };
  if (["UNKNOWN", "BLOCKED", "REJECTED"].includes(local.state ?? ""))
    return {
      label: orderStateLabels[local.state!],
      tone: "warning",
      icon: "error" as const,
    };
  return {
    label: orderStateLabels[local.state ?? ""] ?? "Состояние требует проверки",
    tone: "muted",
    icon: "history" as const,
  };
}
export function PostingTargetStatus({
  local,
  locked,
}: {
  local?: OrderTargetState;
  locked?: boolean;
}) {
  const state = targetPresentation(local, locked);
  return (
    <span className={`orders-target-status ${state.tone}`}>
      <Icon name={state.icon} size="button" />
      {state.label}
    </span>
  );
}
