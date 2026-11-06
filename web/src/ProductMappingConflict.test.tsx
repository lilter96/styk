import { afterEach, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useConfirmation } from "./Confirmation";
import { ProductMappingConflict } from "./ProductMappingConflict";
import type { Product } from "./api";

const product: Product = {
  provider: "ozon",
  kind: "product",
  observed: true,
  shared_inventory: true,
  product_id: "boots",
  offer_id: "5998866",
  name: "Ботинки мужские",
  sku: null,
  archived: false,
  target_id: "table",
  target_kind: "product",
  target_name: "Стол для бильярда",
  match_status: "CONFLICT",
  conflict_reasons: ["DIFFERENT_CANDIDATE"],
  candidate_count: 1,
  candidates_truncated: false,
  candidates: [
    {
      id: "boots-ms",
      kind: "product",
      name: "Ботинки мужские",
      article: "5998866",
      evidence: ["article"],
    },
  ],
};
const id = "a".repeat(32);
const review = {
  Review: { ID: id, ExpiresAt: new Date(Date.now() + 600000).toISOString() },
  Status: "CURRENT",
  ReplacementAllowed: true,
};
const outcome = {
  review_id: id,
  mapping_id: 4,
  mapping_version: 2,
  confirmed_at: "2026-11-06T20:00:00Z",
  evidence_class: "OPERATOR_ATTESTED",
  idempotent_replay: true,
};
const json = (value: unknown) =>
  new Response(JSON.stringify(value), { status: 200 });
function Harness({ value }: { value: Product }) {
  const confirmation = useConfirmation();
  return (
    <ProductMappingConflict
      product={value}
      scope={{ tenant: "t", moy: "m", ozon: "o" }}
      close={() => {}}
      confirmation={confirmation}
    />
  );
}
function mount(value = product) {
  return render(
    <QueryClientProvider
      client={
        new QueryClient({
          defaultOptions: {
            queries: { retry: false },
            mutations: { retry: false },
          },
        })
      }
    >
      <Harness value={value} />
    </QueryClientProvider>,
  );
}
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

it("explains the conflicting pair without loading the whole catalogue or history", async () => {
  const fetcher = vi
    .spyOn(globalThis, "fetch")
    .mockResolvedValue(json({ items: [], has_next: false }));
  mount();
  expect(
    screen.getByRole("heading", { name: "По артикулу найден другой товар" }),
  ).toBeTruthy();
  expect(screen.getByText("Стол для бильярда")).toBeTruthy();
  expect(fetcher).not.toHaveBeenCalled();
  fireEvent.click(
    screen.getByRole("button", { name: "Найти другой товар в МойСклад" }),
  );
  await screen.findByText("Товары не найдены");
  expect(String(fetcher.mock.calls[0][0])).toContain("/api/moysklad-products?");
  expect(
    screen.getByRole("button", { name: "Показать все товары" }),
  ).toBeTruthy();
});

it("requires preflight and explicit acknowledgement; retries the same replacement after a lost reply", async () => {
  let lostReply = true;
  const fetcher = vi
    .spyOn(globalThis, "fetch")
    .mockImplementation(async (path) => {
      if (String(path).endsWith("/replace")) {
        if (lostReply) {
          lostReply = false;
          throw new Error("lost response");
        }
        return json(outcome);
      }
      return json(review);
    });
  mount();
  fireEvent.click(
    screen.getByRole("button", { name: /Ботинки мужские Совпал артикул/ }),
  );
  expect(fetcher).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Проверить замену" }));
  const acknowledgement = await screen.findByRole("checkbox", {
    name: "Я проверил товары и подтверждаю замену текущей связи",
  });
  expect(
    screen.getByRole<HTMLButtonElement>("button", { name: "Заменить связь" })
      .disabled,
  ).toBe(true);
  fireEvent.click(acknowledgement);
  fireEvent.click(screen.getByRole("button", { name: "Заменить связь" }));
  fireEvent.click(
    await screen.findByRole("button", { name: "Уточнить результат" }),
  );
  await screen.findByRole("heading", { name: "Связь заменена" });
  const replacements = fetcher.mock.calls.filter(([path]) =>
    String(path).endsWith("/replace"),
  );
  expect(replacements).toHaveLength(2);
  expect(replacements[0][1]?.body).toEqual(replacements[1][1]?.body);
  expect(JSON.parse(String(replacements[0][1]?.body))).toMatchObject({
    acknowledge: true,
  });
});

it("does not offer a replacement across accounts", () => {
  mount({ ...product, conflict_reasons: ["OTHER_ACCOUNT"] });
  expect(
    screen.getByRole("heading", { name: "Связь относится к другому аккаунту" }),
  ).toBeTruthy();
  expect(screen.queryByRole("button", { name: /Найти другой/ })).toBeNull();
});

it("lets an operator recheck the current target when the Ozon article changed", async () => {
  const fetcher = vi.spyOn(globalThis, "fetch").mockResolvedValue(json(review));
  mount({ ...product, conflict_reasons: ["OFFER_CHANGED"], candidates: [] });
  fireEvent.click(
    screen.getByRole("button", {
      name: "Проверить текущую пару с новым артикулом",
    }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Проверить замену" }));
  await waitFor(() => expect(fetcher).toHaveBeenCalled());
  expect(JSON.parse(String(fetcher.mock.calls[0][1]?.body))).toMatchObject({
    target_id: "table",
  });
});
