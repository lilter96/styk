import { afterEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { Products } from "./App";
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
function setup(status = "CURRENT", conflict = false) {
  let confirmed = false;
  const fetchMock = vi
    .spyOn(globalThis, "fetch")
    .mockImplementation((input, init) => {
      const path = String(input);
      if (path.includes("moysklad-products"))
        return response({
          items: [
            {
              kind: "variant",
              id: "v",
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
        return response({
          mapping_id: 7,
          mapping_version: 1,
          review_id: review.Review.ID,
          evidence_class: "OPERATOR_ATTESTED",
          confirmed_at: "2026-01-01T00:00:00Z",
          idempotent_replay: false,
        });
      }
      if (path.includes("product-mapping-reviews"))
        return response({
          ...review,
          Status: status,
          MappingConflict: conflict,
        });
      if (init?.method === "POST") throw new Error("unexpected post");
      return response({ items: [product], offset: 0, has_next: false });
    });
  render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <MemoryRouter>
        <Products scope={scope} />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return fetchMock;
}
async function createReview() {
  fireEvent.click(await screen.findByRole("button", { name: "Review" }));
  fireEvent.change(screen.getByLabelText("Article, code or identity"), {
    target: { value: "different" },
  });
  fireEvent.click(await screen.findByRole("button", { name: "variant · v" }));
  expect(screen.getAllByText(/parent product: parent/i).length).toBeGreaterThan(
    0,
  );
  fireEvent.click(screen.getByRole("button", { name: "Create review" }));
  await screen.findByText("Durable review");
}
it("creates a manual variant review, requires acknowledgement and refreshes history", async () => {
  const mock = setup();
  await createReview();
  const button = screen.getByRole("button", {
    name: "Confirm mapping",
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
  fireEvent.click(screen.getByLabelText(/I explicitly confirm/));
  fireEvent.click(button);
  fireEvent.click(button);
  await screen.findByText(/Mapping 7, version 1/);
  expect(
    mock.mock.calls.filter(([p]) => String(p).endsWith("/confirm")),
  ).toHaveLength(1);
});
for (const status of ["EXPIRED", "STALE_SOURCE", "STALE_MAPPING"])
  it(`${status} review cannot be confirmed`, async () => {
    setup(status);
    await createReview();
    fireEvent.click(screen.getByLabelText(/I explicitly confirm/));
    expect(
      (
        screen.getByRole("button", {
          name: "Confirm mapping",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
  });
it("mapping conflict prevents confirmation", async () => {
  setup("CURRENT", true);
  await createReview();
  fireEvent.click(screen.getByLabelText(/I explicitly confirm/));
  expect(
    (
      screen.getByRole("button", {
        name: "Confirm mapping",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
});
