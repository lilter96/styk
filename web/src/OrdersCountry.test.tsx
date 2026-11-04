import "@testing-library/jest-dom/vitest";
import { afterEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { OrdersCountry } from "./OrdersCountry";
import type { OrderPosting } from "./OrdersTypes";
const scope = { tenant: "synthetic", moy: "moy", ozon: "ozon" };
const posting = {
  posting_number: "P-1",
  evidence: { revision: "rev1" },
  lines: [
    { identity: { sku: "9007199254740993", offer_id: "synthetic-offer" } },
  ],
} as OrderPosting;
const dictionary = {
  scope: {
    tenant: scope.tenant,
    moysklad_account: scope.moy,
    ozon_account: scope.ozon,
  },
  posting_number: "P-1",
  source_revision: "rev1",
  required_skus: ["9007199254740993"],
  countries: [{ name: "Тестовая страна из API", country_iso_code: "BY" }],
};
const json = (value: unknown) =>
  new Response(JSON.stringify(value), {
    headers: { "Content-Type": "application/json" },
  });
function mount(value = dictionary) {
  const fetcher = vi.fn(async () => json(value));
  vi.stubGlobal("fetch", fetcher);
  const changed = vi.fn();
  render(
    <QueryClientProvider
      client={
        new QueryClient({
          defaultOptions: { queries: { retry: false, gcTime: 0 } },
        })
      }
    >
      <OrdersCountry
        scope={scope}
        posting={posting}
        disabled={false}
        onChange={changed}
      />
    </QueryClientProvider>,
  );
  return { fetcher, changed };
}
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});
it("uses only exact required SKU and dictionary country after factual confirmation", async () => {
  const { fetcher, changed } = mount();
  await screen.findByRole("option", { name: "Тестовая страна из API (BY)" });
  const confirm = screen.getByLabelText(
    "Подтверждаю фактическую страну происхождения выбранного товара по имеющимся сведениям.",
  );
  expect(confirm).toBeDisabled();
  fireEvent.change(
    screen.getByLabelText("Товар, для которого Ozon требует страну"),
    { target: { value: "9007199254740993" } },
  );
  fireEvent.change(screen.getByLabelText("Страна из справочника Ozon"), {
    target: { value: "BY" },
  });
  expect(changed).toHaveBeenLastCalledWith(undefined);
  fireEvent.click(confirm);
  expect(changed).toHaveBeenLastCalledWith({
    sku: "9007199254740993",
    iso: "BY",
    confirmed: true,
  });
  expect(fetcher).toHaveBeenCalledTimes(1);
  expect(
    screen.getByText(/это не независимое подтверждение страны/),
  ).toBeVisible();
});
it("does not load provider dictionary on every typed search character", async () => {
  const { fetcher } = mount();
  await screen.findByRole("option", { name: "Тестовая страна из API (BY)" });
  fireEvent.change(screen.getByLabelText("Поиск страны"), {
    target: { value: "Страна" },
  });
  expect(fetcher).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByText("Найти страну"));
  await vi.waitFor(() => expect(fetcher).toHaveBeenCalledTimes(2));
});
it("fails closed for dictionary evidence from another tenant or unknown SKU", async () => {
  mount({ ...dictionary, scope: { ...dictionary.scope, tenant: "other" } });
  await screen.findByRole("alert");
  expect(
    screen.queryByRole("option", { name: "Тестовая страна из API (BY)" }),
  ).not.toBeInTheDocument();
});
