import { StrictMode } from "react";
import { afterEach, expect, it, vi } from "vitest";
import { act, cleanup, render, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { OrdersConfig } from "./OrdersConfig";
import {
  orderScopeBody,
  type OrdersConfigData,
  type OrderScope,
} from "./OrdersTypes";

const scope: OrderScope = { tenant: "synthetic", moy: "moy", ozon: "ozon" };
const config: OrdersConfigData = {
  scope: orderScopeBody(scope),
  revision: 1,
  enabled: false,
  test_enabled: false,
  publication_available: false,
  interval_seconds: 300,
  source_freshness_seconds: 300,
  preview_ttl_seconds: 300,
  pii_enabled: false,
  pii_retention_seconds: 0,
  organization_id: "",
  agent_id: "",
  store_id: "",
  state_id: "",
  sales_channel_id: "",
  currency_id: "",
  field_mappings: [],
  status_mappings: [],
};
const metadata = {
  scope: orderScopeBody(scope),
  organizations: [],
  agents: [],
  stores: [],
  states: [],
  sales_channels: [],
  currencies: [],
  attributes: [],
};
const json = (value: unknown) =>
  new Response(JSON.stringify(value), { status: 200 });
let clients: QueryClient[] = [];
afterEach(() => {
  cleanup();
  clients.forEach((client) => client.clear());
  clients = [];
  vi.unstubAllGlobals();
});

function fixture() {
  const attempts: {
    signal?: AbortSignal | null;
    resolve: (r: Response) => void;
  }[] = [];
  const fetcher = vi.fn((path: string, init?: RequestInit) => {
    if (!new URL(path, "http://localhost").pathname.endsWith("/metadata"))
      return Promise.resolve(
        json({ scope: orderScopeBody(scope), revision: 0, attributes: [] }),
      );
    return new Promise<Response>((resolve, reject) => {
      const signal = init?.signal;
      attempts.push({ signal, resolve });
      signal?.addEventListener(
        "abort",
        () => reject(new DOMException("Aborted", "AbortError")),
        { once: true },
      );
    });
  });
  vi.stubGlobal("fetch", fetcher);
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, refetchOnWindowFocus: false, staleTime: 30000 },
    },
  });
  clients.push(client);
  const content = (
    <QueryClientProvider client={client}>
      <OrdersConfig
        scope={scope}
        config={config}
        onSaved={() => {}}
        onDirty={() => {}}
      />
    </QueryClientProvider>
  );
  return { attempts, client, content };
}

it("performs one metadata request in normal rendering and reuses the fresh scoped result after remount", async () => {
  const f = fixture();
  const mounted = render(f.content);
  await waitFor(() => expect(f.attempts).toHaveLength(1));
  await act(async () => {
    f.attempts[0].resolve(json(metadata));
  });
  await waitFor(() =>
    expect(f.client.getQueryData(["orders-metadata", scope])).toEqual(metadata),
  );
  mounted.rerender(
    <QueryClientProvider client={f.client}>
      <OrdersConfig
        scope={{ ...scope }}
        config={{ ...config }}
        onSaved={() => {}}
        onDirty={() => {}}
      />
    </QueryClientProvider>,
  );
  mounted.unmount();
  render(f.content);
  await act(async () => {});
  expect(f.attempts).toHaveLength(1);
});

it("development StrictMode aborts the first metadata subscription and creates one replacement, without retrying successful reads", async () => {
  const f = fixture();
  render(<StrictMode>{f.content}</StrictMode>);
  await waitFor(() => expect(f.attempts).toHaveLength(2));
  expect(f.attempts[0].signal?.aborted).toBe(true);
  expect(f.attempts[1].signal?.aborted).toBe(false);
  await act(async () => {
    f.attempts[1].resolve(json(metadata));
  });
  await waitFor(() =>
    expect(f.client.getQueryData(["orders-metadata", scope])).toEqual(metadata),
  );
  expect(f.attempts).toHaveLength(2);
});

it("cancels an in-flight metadata read on actual navigation away", async () => {
  const f = fixture();
  const mounted = render(f.content);
  await waitFor(() => expect(f.attempts).toHaveLength(1));
  mounted.unmount();
  expect(f.attempts[0].signal?.aborted).toBe(true);
});
