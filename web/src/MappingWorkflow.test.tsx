import { afterEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { ProductMappingPage as Products } from "./ProductMappingPage";
import type { Product } from "./api";
import { ConfirmationNotice, useConfirmation } from "./Confirmation";
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});
const scope = { tenant: "tenant", moy: "moy", ozon: "ozon" };
const review = {
  Review: {
    ID: "a".repeat(32),
    Source: { ID: "p" },
    Target: { ID: "v", Kind: "variant" },
    EvidenceClass: "OPERATOR_ATTESTED",
    CreatedAt: "2026-01-01T00:00:00Z",
    ExpiresAt: "2099-01-01T00:00:00Z",
    OzonImportID: 1,
    MoyskladImportID: 2,
    ExistingMappingVersion: null,
  },
  Status: "CURRENT",
  MappingConflict: false,
  PrincipalType: "LOCAL_OPERATOR_SESSION",
};
const product = {
  product_id: "p",
  offer_id: "offer",
  sku: null,
  archived: false,
  target_kind: null,
  target_id: null,
  match_status: "NO_CANDIDATE",
  candidate_count: 0,
  candidates: [],
  candidates_truncated: false,
};
const response = (data: unknown) =>
  Promise.resolve(
    new Response(JSON.stringify(data), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    }),
  );
function setup(
  status = "CURRENT",
  conflict = false,
  lost = false,
  counts: Record<string, number> = {},
  products: Product[] = [product],
  withNotice = false,
) {
  let confirmed = false;
  let confirmationAttempts = 0;
  const fetchMock = vi
    .spyOn(globalThis, "fetch")
    .mockImplementation((input, init) => {
      const path = String(input);
      if (path.includes("/api/diagnostics"))
        return response({ mapping_counts: counts });
      if (path.includes("moysklad-products"))
        return response({
          items: [
            {
              kind: "variant",
              id: "v",
              name: "Synthetic variant",
              article: "different",
              code: "code",
              parent_id: "parent",
            },
          ],
          offset: 0,
          has_next: false,
        });
      if (path.includes("product-mapping-history"))
        return response({
          items: confirmed
            ? [
                {
                  mapping_id: 7,
                  version: 1,
                  offer_id: "offer",
                  target_kind: "variant",
                  target_id: "v",
                  confirmed_at: "2026-01-01T00:00:00Z",
                  provenance: "operator",
                  ozon_import_id: 1,
                  moysklad_import_id: 2,
                  evidence_class: "OPERATOR_ATTESTED",
                },
              ]
            : [],
          offset: 0,
          has_next: false,
        });
      if (path.endsWith("/confirm")) {
        confirmed = true;
        confirmationAttempts++;
        if (lost && confirmationAttempts === 1)
          return Promise.reject(new Error("synthetic lost response"));
        return response({
          mapping_id: 7,
          mapping_version: 1,
          review_id: review.Review.ID,
          evidence_class: "OPERATOR_ATTESTED",
          confirmed_at: "2026-01-01T00:00:00Z",
          idempotent_replay: lost && confirmationAttempts > 1,
        });
      }
      if (path.includes("product-mapping-reviews"))
        return response({
          ...review,
          Status: status,
          MappingConflict: conflict,
        });
      if (init?.method === "POST") throw new Error("unexpected post");
      return response({ items: products, offset: 0, has_next: false });
    });
  render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <MemoryRouter>
        {withNotice ? <ProductsWithNotice /> : <Products scope={scope} />}
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return fetchMock;
}
function ProductsWithNotice() {
  const confirmation = useConfirmation();
  return (
    <>
      <ConfirmationNotice
        confirmation={confirmation}
        scope={scope}
        restore={() => {}}
      />
      <Products scope={scope} confirmation={confirmation} />
    </>
  );
}
async function createReview() {
  fireEvent.click(
    await screen.findByRole("button", { name: "Сопоставить offer" }),
  );
  fireEvent.change(screen.getByLabelText("Поиск товара МойСклад"), {
    target: { value: "different" },
  });
  fireEvent.click(
    await screen.findByRole("button", {
      name: "Synthetic variant, вариант",
    }),
  );
  expect(screen.getAllByText("Synthetic variant").length).toBeGreaterThan(1);
  expect(screen.queryByText("Технические данные товаров")).toBeNull();
  fireEvent.click(
    screen.getByRole("button", { name: "Проверить соответствие" }),
  );
  await screen.findByText("Проверка готова к подтверждению");
}
it("creates a manual variant review, requires acknowledgement and refreshes history", async () => {
  const mock = setup();
  await createReview();
  const button = screen.getByRole("button", {
    name: "Сопоставить",
  }) as HTMLButtonElement;
  expect(button.disabled).toBe(true);
  expect(
    mock.mock.calls.filter(([p]) => String(p).endsWith("/confirm")),
  ).toHaveLength(0);
  const request = mock.mock.calls.find(
    ([p, i]) =>
      String(p).endsWith("/product-mapping-reviews") && i?.method === "POST",
  );
  expect(JSON.parse(String(request?.[1]?.body))).toEqual({
    tenant: "tenant",
    moysklad_account: "moy",
    ozon_account: "ozon",
    product_id: "p",
    target_kind: "variant",
    target_id: "v",
    evidence_class: "OPERATOR_ATTESTED",
  });
  fireEvent.click(
    screen.getByLabelText(/Подтверждаю, что это один и тот же товар/),
  );
  fireEvent.click(button);
  fireEvent.click(button);
  fireEvent.click(screen.getByText("Дополнительно"));
  fireEvent.click(screen.getByText("История сопоставлений"));
  await screen.findByText(/Соответствие 7, версия 1/);
  expect(
    mock.mock.calls.filter(([p]) => String(p).endsWith("/confirm")),
  ).toHaveLength(1);
});
for (const status of ["EXPIRED", "STALE_SOURCE", "STALE_MAPPING"])
  it(`${status} review cannot be confirmed`, async () => {
    setup(status);
    await createReview();
    fireEvent.click(
      screen.getByLabelText(/Подтверждаю, что это один и тот же товар/),
    );
    expect(
      (
        screen.getByRole("button", {
          name: "Сопоставить",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
  });
it("mapping conflict prevents confirmation", async () => {
  setup("CURRENT", true);
  await createReview();
  fireEvent.click(
    screen.getByLabelText(/Подтверждаю, что это один и тот же товар/),
  );
  expect(
    (
      screen.getByRole("button", {
        name: "Сопоставить",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
});

it("retries the exact scoped confirmation after a lost response from inside the dialog", async () => {
  const mock = setup("CURRENT", false, true);
  await createReview();
  fireEvent.click(
    screen.getByLabelText(/Подтверждаю, что это один и тот же товар/),
  );
  fireEvent.click(screen.getByRole("button", { name: "Сопоставить" }));
  const retry = await screen.findByRole("button", {
    name: "Повторить тот же запрос",
  });
  expect(
    (
      screen.getByRole("button", {
        name: "Сопоставить",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  fireEvent.click(retry);
  await screen.findByText(
    "Соответствие сохранено. Это подтверждение не включает публикацию остатков.",
  );
  const calls = mock.mock.calls.filter(([path]) =>
    String(path).endsWith("/confirm"),
  );
  expect(calls).toHaveLength(2);
  expect(calls[1][1]?.body).toBe(calls[0][1]?.body);
  expect(String(calls[1][0])).toBe(String(calls[0][0]));
});

it("uses latest-catalog counts for progress instead of historical mappings or the visible page", async () => {
  setup("CURRENT", false, false, {
    confirmed_products: 99,
    unconfirmed_products: 5,
    catalog_products: 9,
    catalog_confirmed_products: 4,
  });
  await screen.findByText("Сопоставлено 4 из 9");
  const progress = screen.getByRole("progressbar", {
    name: "Сопоставление последнего каталога",
  });
  expect(progress.getAttribute("aria-valuemax")).toBe("9");
  expect(progress.getAttribute("aria-valuenow")).toBe("4");
  expect(screen.queryByText("Подтверждено соответствий: 99")).toBeNull();
});

it("queues several suggestions for individual review without confirming them", async () => {
  const variants = [
    {
      ...product,
      product_id: "p1",
      offer_id: "one",
      match_status: "ONE_CANDIDATE",
      candidate_count: 1,
      candidates: [
        { kind: "variant", id: "v1", name: "Размер S", evidence: ["article"] },
      ],
    },
    {
      ...product,
      product_id: "p2",
      offer_id: "two",
      match_status: "ONE_CANDIDATE",
      candidate_count: 1,
      candidates: [
        { kind: "product", id: "p3", name: "Другой товар", evidence: ["code"] },
      ],
    },
    {
      ...product,
      product_id: "archived",
      offer_id: "archived",
      archived: true,
      match_status: "ONE_CANDIDATE",
      candidate_count: 1,
      candidates: [{ kind: "product", id: "p4", evidence: ["code"] }],
    },
  ];
  const fetchMock = vi
    .spyOn(globalThis, "fetch")
    .mockImplementation((input, init) => {
      if (init?.method === "POST")
        throw new Error("review selection must not write");
      const path = String(input);
      if (path.includes("/api/diagnostics"))
        return response({ mapping_counts: {} });
      if (path.includes("moysklad-products"))
        return response({ items: [], offset: 0, has_next: false });
      return response({ items: variants, offset: 0, has_next: false });
    });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <Products scope={scope} />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  fireEvent.click(await screen.findByLabelText("Выбрать one для проверки"));
  fireEvent.click(screen.getByLabelText("Выбрать two для проверки"));
  expect(screen.queryByLabelText("Выбрать archived для проверки")).toBeNull();
  fireEvent.click(screen.getByText("Выбрано для проверки: 2"));
  expect(screen.getByText(/Размер S \(вариант\)/)).toBeTruthy();
  expect(screen.getByRole("button", { name: "Проверить one" })).toBeTruthy();
  expect(screen.getByRole("button", { name: "Проверить two" })).toBeTruthy();
  expect(fetchMock.mock.calls.some(([, init]) => init?.method === "POST")).toBe(
    false,
  );
  client.clear();
});

it("keeps successful and remaining items visible after an individual confirmation", async () => {
  const candidates = [
    {
      kind: "variant",
      id: "v",
      name: "Synthetic variant",
      evidence: ["article"],
    },
  ];
  const mock = setup("CURRENT", false, false, {}, [
    {
      ...product,
      candidates,
      candidate_count: 1,
      match_status: "ONE_CANDIDATE",
    },
    {
      ...product,
      product_id: "p2",
      offer_id: "second",
      candidates,
      candidate_count: 1,
      match_status: "ONE_CANDIDATE",
    },
  ]);
  fireEvent.click(
    await screen.findByRole("button", {
      name: "Выбрать предложения на странице",
    }),
  );
  fireEvent.click(screen.getByText("Выбрано для проверки: 2"));
  await createReview();
  fireEvent.click(
    screen.getByLabelText(/Подтверждаю, что это один и тот же товар/),
  );
  fireEvent.click(screen.getByRole("button", { name: "Сопоставить" }));
  await screen.findByText(
    "Соответствие сохранено. Это подтверждение не включает публикацию остатков.",
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Закрыть сопоставление" }),
  );
  expect(
    screen.getByText("Подтверждено: 1 · Осталось проверить: 1"),
  ).toBeTruthy();
  expect(screen.getByText("Подтверждено: offer")).toBeTruthy();
  expect(screen.getByRole("button", { name: "Проверить second" })).toBeTruthy();
  expect(
    mock.mock.calls.filter(([path]) => String(path).endsWith("/confirm")),
  ).toHaveLength(1);
});

it("shows confirmed target and Ozon names supplied by the catalog", async () => {
  setup("CURRENT", false, false, {}, [
    {
      ...product,
      name: "Название Ozon",
      target_id: "outside-first-page",
      target_kind: "product",
      target_name: "Название МойСклад",
      target_article: "ART-42",
      match_status: "CONFIRMED",
    },
  ]);
  expect(await screen.findByText("Название Ozon")).toBeTruthy();
  expect(screen.getByText("Название МойСклад")).toBeTruthy();
  expect(screen.getByText("ART-42")).toBeTruthy();
  expect(screen.getByText("Offer ID: offer")).toBeTruthy();
});

it("retains partial queue progress and retries an uncertain confirmation only on request", async () => {
  const candidates = [
    {
      kind: "variant",
      id: "v",
      name: "Synthetic variant",
      evidence: ["article"],
    },
  ];
  const mock = setup(
    "CURRENT",
    false,
    true,
    {},
    [
      {
        ...product,
        candidates,
        candidate_count: 1,
        match_status: "ONE_CANDIDATE",
      },
      {
        ...product,
        product_id: "p2",
        offer_id: "second",
        candidates,
        candidate_count: 1,
        match_status: "ONE_CANDIDATE",
      },
    ],
    true,
  );
  fireEvent.click(
    await screen.findByRole("button", {
      name: "Выбрать предложения на странице",
    }),
  );
  fireEvent.click(screen.getByText("Выбрано для проверки: 2"));
  await createReview();
  fireEvent.click(
    screen.getByLabelText(/Подтверждаю, что это один и тот же товар/),
  );
  fireEvent.click(screen.getByRole("button", { name: "Сопоставить" }));
  await screen.findByRole("button", { name: "Повторить тот же запрос" });
  fireEvent.click(
    screen.getByRole("button", { name: "Закрыть сопоставление" }),
  );
  expect(
    screen.getByText("Подтверждено: 0 · Осталось проверить: 2"),
  ).toBeTruthy();
  expect(
    (
      screen.getByRole("button", {
        name: "Проверить second",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  expect(
    mock.mock.calls.filter(([path]) => String(path).endsWith("/confirm")),
  ).toHaveLength(1);
  fireEvent.click(
    screen.getByRole("button", { name: "Повторить то же подтверждение" }),
  );
  await screen.findByText("Подтверждено: 1 · Осталось проверить: 1");
  expect(screen.getByRole("button", { name: "Проверить second" })).toBeTruthy();
  const calls = mock.mock.calls.filter(([path]) =>
    String(path).endsWith("/confirm"),
  );
  expect(calls).toHaveLength(2);
  expect(calls[1][1]?.body).toBe(calls[0][1]?.body);
});
