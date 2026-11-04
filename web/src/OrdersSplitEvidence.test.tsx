import { afterEach, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { OrdersSplitEvidence } from "./OrdersSplitEvidence";

afterEach(cleanup);

it("preserves exact identities and separates pending children from checked contents", () => {
  render(
    <OrdersSplitEvidence
      receipt={{
        state: "POLLING",
        posting_numbers: ["child-9007199254740993", "child-b"],
        observed_postings: [
          {
            posting_number: "child-9007199254740993",
            evidence: { observed_at: "2026-11-04T10:00:00Z" },
          },
        ],
      }}
    />,
  );
  expect(screen.getByText("child-9007199254740993")).toBeTruthy();
  expect(screen.getByText("Посылки после разделения (2)")).toBeTruthy();
  expect(screen.getByText("Ожидаем подтверждения состава")).toBeTruthy();
  expect(screen.getByText(/Состав проверен:/)).toBeTruthy();
});

it("does not invent children before an acknowledgement", () => {
  const { container } = render(
    <OrdersSplitEvidence receipt={{ state: "UNKNOWN" }} />,
  );
  expect(container.textContent).toBe("");
});
