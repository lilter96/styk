import "@testing-library/jest-dom/vitest";
import { afterEach, expect, it } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { OrdersNativePolicy, nativePolicyValid } from "./OrdersNativePolicy";
import { OrdersNativeSummary, nativeDocumentURL } from "./OrdersNativeSummary";
import type { NativeDocumentPolicy } from "./OrdersNativeTypes";
import type { OrderCommand } from "./OrdersTypes";

afterEach(cleanup);
it("keeps an empty server policy editable without granting document actions", () => {
  const policy: NativeDocumentPolicy = {
    enabled: true,
    allow_posted: false,
    actions: null,
    maximum_quantity: "10",
    event_max_age_seconds: 3600,
    attested_at: "",
    valid_until: "",
  };
  expect(nativePolicyValid(policy)).toBe(false);
  let latest: NativeDocumentPolicy | undefined;
  render(
    <OrdersNativePolicy
      value={policy}
      disabled={false}
      onChange={(next) => {
        latest = next;
      }}
    />,
  );
  expect(screen.getByLabelText("Отгрузка")).not.toBeChecked();
  expect(
    screen.getByLabelText("Возврат покупателя по связанной отгрузке"),
  ).not.toBeChecked();
  fireEvent.click(screen.getByLabelText("Отгрузка"));
  expect(latest?.actions).toEqual(["demand_create"]);
  expect(latest?.allow_posted).toBe(false);
});
it("validates physical-event policy expiry and bounded quantities", () => {
  const policy: NativeDocumentPolicy = {
    enabled: true,
    allow_posted: false,
    actions: ["demand_create"],
    maximum_quantity: "10",
    event_max_age_seconds: 3600,
    attested_at: "",
    valid_until: "",
  };
  expect(nativePolicyValid(policy)).toBe(true);
  expect(nativePolicyValid({ ...policy, maximum_quantity: "1001" })).toBe(
    false,
  );
  expect(nativePolicyValid({ ...policy, allow_unposted_parent: true })).toBe(
    false,
  );
  expect(nativePolicyValid({ ...policy, allow_posted: true })).toBe(false);
  expect(
    nativePolicyValid({
      ...policy,
      allow_posted: true,
      attested_at: new Date().toISOString(),
      valid_until: new Date(Date.now() + 3600000).toISOString(),
    }),
  ).toBe(true);
});
it("enabling document preparation does not enable posting or fabricate a physical event", () => {
  let latest: NativeDocumentPolicy | undefined;
  function Harness() {
    const [value, setValue] = useState<NativeDocumentPolicy>();
    return (
      <OrdersNativePolicy
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
  fireEvent.click(
    screen.getByLabelText("Разрешить подготовку складских документов"),
  );
  expect(latest?.enabled).toBe(true);
  expect(latest?.allow_posted).toBe(false);
  expect(latest?.allow_unposted_parent).toBe(false);
  const unpostedLabel =
    "Разрешить отгрузки и возвраты по непроведённому заказу МойСклад";
  expect(screen.queryByLabelText(unpostedLabel)).not.toBeInTheDocument();
  const checkbox = screen.getByLabelText(
    "На один час разрешаю отдельное ручное подтверждение проведения по реальным складским событиям.",
  );
  fireEvent.click(checkbox);
  const expiry = latest?.valid_until;
  expect(latest?.allow_posted).toBe(true);
  expect(Date.parse(expiry!)).toBeGreaterThan(Date.now());
  expect(screen.getByLabelText(unpostedLabel)).not.toBeChecked();
  fireEvent.click(screen.getByLabelText(unpostedLabel));
  expect(latest?.allow_unposted_parent).toBe(true);
  fireEvent.change(
    screen.getByLabelText("Максимальное количество в складском документе"),
    { target: { value: "7" } },
  );
  expect(latest?.valid_until).toBe(expiry);
  expect(latest?.allow_unposted_parent).toBe(true);
  fireEvent.click(checkbox);
  expect(latest?.allow_unposted_parent).toBe(false);
  expect(screen.queryByLabelText(unpostedLabel)).not.toBeInTheDocument();
  fireEvent.click(checkbox);
  expect(screen.getByLabelText(unpostedLabel)).not.toBeChecked();
  fireEvent.click(screen.getByLabelText(unpostedLabel));
  fireEvent.click(
    screen.getByLabelText("Разрешить подготовку складских документов"),
  );
  expect(latest?.enabled).toBe(false);
  expect(latest?.allow_posted).toBe(false);
  expect(latest?.allow_unposted_parent).toBe(false);
});
it("explains draft semantics and links only exact supported native document identities", () => {
  const command = {
    scope: {
      tenant: "native-policy",
      moysklad_account: "moy",
      ozon_account: "ozon",
    },
    id: "native-command",
    posting_number: "SYNTHETIC-1",
    correlation: {},
    action: "demand_create",
    native_movement: {
      applicable: { presence: "zero", value: false },
      organization_id: "org",
      agent_id: "agent",
      store_id: "store",
      currency_id: "rub",
      moment: new Date().toISOString(),
      lines: [],
    },
  } as OrderCommand;
  render(<OrdersNativeSummary command={command} />);
  expect(screen.getByText(/Будет создан непроведённый документ/)).toBeVisible();
  const id = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee";
  expect(nativeDocumentURL(command, id)).toBe(
    `https://online.moysklad.ru/app/#demand/edit?id=${id}`,
  );
  expect(
    nativeDocumentURL(command, "https://internal.example"),
  ).toBeUndefined();
  expect(
    nativeDocumentURL({ ...command, action: "mirror_update" }, id),
  ).toBeUndefined();
});
