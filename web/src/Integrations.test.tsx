import { afterEach, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { Integrations } from "./Integrations";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});
function setup(failure = "") {
  let verified = false,
    registered = false;
  const fetcher = vi
    .spyOn(globalThis, "fetch")
    .mockImplementation(async (input) => {
      const path = String(input);
      if (path.endsWith("/probe")) {
        verified = true;
        return new Response("{}", { status: 200 });
      }
      if (path.endsWith("/register")) {
        if (failure)
          return new Response(JSON.stringify({ error: failure }), {
            status: failure === "OWNERSHIP_CONFLICT" ? 409 : 503,
          });
        registered = true;
        return new Response('{"status":"REGISTERED"}', { status: 200 });
      }
      return new Response(
        JSON.stringify({
          providers: [
            {
              provider: "moysklad",
              status: verified ? "VERIFIED" : "CONFIGURED_NOT_VERIFIED",
              registration: registered ? "REGISTERED" : "NOT_REGISTERED",
              check: verified
                ? {
                    checked_at: new Date().toISOString(),
                    identity: "NOT_ESTABLISHED",
                  }
                : undefined,
            },
            {
              provider: "ozon",
              status: "NOT_CONFIGURED",
              registration: "NOT_CHECKED",
            },
          ],
        }),
        { status: 200 },
      );
    });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <Integrations tenant="synthetic-tenant" accounts={[]} />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return fetcher;
}
it("does not probe automatically and requires an explicit acknowledgement before registration", async () => {
  const fetcher = setup();
  const card = (
    await screen.findByRole("heading", { name: "MoySklad" })
  ).closest("article")!;
  expect(within(card).getByText("CONFIGURED_NOT_VERIFIED")).toBeTruthy();
  expect(
    fetcher.mock.calls.filter(([p]) => String(p).endsWith("/probe")),
  ).toHaveLength(0);
  expect(
    (
      within(card).getByRole("button", {
        name: "Register account",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  fireEvent.click(
    within(card).getByRole("button", { name: "Test connection" }),
  );
  await within(card).findByText("VERIFIED");
  expect(within(card).getByText(/not independently established/)).toBeTruthy();
  fireEvent.click(within(card).getByLabelText(/I attest/));
  fireEvent.click(
    within(card).getByRole("button", { name: "Register account" }),
  );
  await within(card).findByText(/Configured account: REGISTERED/);
  expect(
    fetcher.mock.calls.filter(([p]) => String(p).endsWith("/register")),
  ).toHaveLength(1);
  expect(
    within(card).getByRole("link", { name: "Products" }).getAttribute("href"),
  ).toBe("/products");
  const ozon = screen
    .getByRole("heading", { name: "Ozon" })
    .closest("article")!;
  expect(
    (
      within(ozon).getByRole("button", {
        name: "Test connection",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
});
it("reconciles an unknown registration outcome before allowing another attempt", async () => {
  setup("REGISTRATION_OUTCOME_UNKNOWN");
  const card = (
    await screen.findByRole("heading", { name: "MoySklad" })
  ).closest("article")!;
  fireEvent.click(
    within(card).getByRole("button", { name: "Test connection" }),
  );
  await within(card).findByText("VERIFIED");
  fireEvent.click(within(card).getByLabelText(/I attest/));
  fireEvent.click(
    within(card).getByRole("button", { name: "Register account" }),
  );
  await screen.findByText(/outcome is unknown/);
  expect(
    (
      within(card).getByRole("button", {
        name: "Register account",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  fireEvent.click(
    screen.getByRole("button", { name: "Check registration outcome" }),
  );
  await screen.findByText(/status was checked/);
});
it("shows a conflicting registration without transferring ownership", async () => {
  setup("OWNERSHIP_CONFLICT");
  const card = (
    await screen.findByRole("heading", { name: "MoySklad" })
  ).closest("article")!;
  fireEvent.click(
    within(card).getByRole("button", { name: "Test connection" }),
  );
  await within(card).findByText("VERIFIED");
  fireEvent.click(within(card).getByLabelText(/I attest/));
  fireEvent.click(
    within(card).getByRole("button", { name: "Register account" }),
  );
  await screen.findByText(/cannot be transferred/);
});

it("shows persisted import age and counts without claiming live connectivity", async () => {
  vi.spyOn(globalThis, "fetch").mockResolvedValue(
    new Response(
      JSON.stringify({
        providers: [
          {
            provider: "moysklad",
            status: "CONFIGURED_NOT_VERIFIED",
            registration: "REGISTERED",
            import: {
              present: true,
              completed: true,
              observed_at: "2020-01-01T00:00:00Z",
              age: "10h",
              counts: { products: 3, warehouses: 2, stock_rows: 0 },
            },
          },
        ],
      }),
      { status: 200 },
    ),
  );
  render(
    <QueryClientProvider client={new QueryClient()}>
      <MemoryRouter>
        <Integrations tenant="synthetic-tenant" accounts={[]} />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  expect(await screen.findByText(/Stale observation/)).toBeTruthy();
  expect(screen.getByText(/Live connection not checked/)).toBeTruthy();
  expect(screen.getByText(/Products 3/)).toBeTruthy();
});
