import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import {
  OrdersComposition,
  OrdersCompositionSummary,
} from "./OrdersComposition";

afterEach(cleanup);

it("requires explicit preparation consent and sends only one exact posting to preview", () => {
  const preview = vi.fn();
  const changed = vi.fn();
  render(
    <OrdersComposition
      postingNumber="SYNTHETIC-1"
      disabled={false}
      onPreview={preview}
      onDraftChange={changed}
    />,
  );
  const button = screen.getByRole("button", {
    name: "Проверить замену состава",
  });
  expect(button).toBeDisabled();
  expect(preview).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("checkbox"));
  expect(changed).toHaveBeenCalledOnce();
  expect(preview).not.toHaveBeenCalled();
  fireEvent.click(button);
  expect(preview).toHaveBeenCalledExactlyOnceWith({
    action: "mirror_composition_update",
    posting_number: "SYNTHETIC-1",
    confirm_replacement: true,
  });
  fireEvent.click(screen.getByRole("checkbox"));
  expect(button).toBeDisabled();
});

it("does not allow composition review while the target is locked", () => {
  render(
    <OrdersComposition
      postingNumber="SYNTHETIC-2"
      disabled
      onPreview={vi.fn()}
      onDraftChange={vi.fn()}
    />,
  );
  expect(screen.getByRole("checkbox")).toBeDisabled();
  expect(screen.getByRole("button")).toBeDisabled();
});

it("distinguishes added and removed identities without interpreting SKU as a number", () => {
  render(
    <OrdersCompositionSummary
      value={{
        confirm_replacement: true,
        retained: [
          { source_identity: { sku: "1" }, native_position_id: "native-1" },
        ],
        added: [{ sku: "9007199254740993" }],
        removed: [
          {
            source_identity: { offer_id: "OLD-OFFER" },
            native_position_id: "native-2",
          },
        ],
      }}
    />,
  );
  expect(screen.getByText("Добавить: 9007199254740993")).toBeVisible();
  expect(screen.getByText("Удалить: OLD-OFFER")).toBeVisible();
  expect(screen.getByText(/Изменения ещё не отправлены/)).toBeVisible();
});
