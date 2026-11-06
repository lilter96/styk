import { StrictMode } from "react";
import { afterEach, expect, it, vi } from "vitest";
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { Orders } from "./Orders";
import { OrdersConfig } from "./OrdersConfig";
import { useOrdersMetadata } from "./useOrdersMetadata";
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
  organizations: [{ id: "org", name: "Своя организация" }],
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

function fixture(revision = 1) {
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
        config={{ ...config, revision }}
        onSaved={() => {}}
        onDirty={() => {}}
      />
    </QueryClientProvider>
  );
  return { attempts, client, content };
}

function MetadataProbe({
  currentScope = scope,
  enabled = true,
}: {
  currentScope?: OrderScope;
  enabled?: boolean;
}) {
  const query = useOrdersMetadata(currentScope, enabled);
  return (
    <p>
      {query.data?.organizations[0]?.name ??
        (query.isError ? "Ошибка справочников" : "Не загружено")}
    </p>
  );
}

it("does not read provider metadata from the orders table, including remount", async () => {
  const f = fixture();
  const table = (
    <QueryClientProvider client={f.client}>
      <Orders scope={scope} />
    </QueryClientProvider>
  );
  const mounted = render(<StrictMode>{table}</StrictMode>);
  await act(async () => {});
  expect(f.attempts).toHaveLength(0);
  mounted.unmount();
  render(table);
  await act(async () => {});
  expect(f.attempts).toHaveLength(0);
});

it("reads when the settings workspace mounts once and reuses the fresh scoped result after remount", async () => {
  const f = fixture();
  const mounted = render(f.content);
  await waitFor(() => expect(f.attempts).toHaveLength(1));
  await act(async () => {
    f.attempts[0].resolve(json(metadata));
  });
  await waitFor(() =>
    expect(f.client.getQueryData(["orders-metadata", scope])).toEqual(metadata),
  );
  mounted.unmount();
  render(f.content);
  await act(async () => {});
  expect(f.attempts).toHaveLength(1);
});

it("keeps first setup open and preserves cancellable StrictMode subscription behavior", async () => {
  const f = fixture(0);
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

it("shares one scoped read between an on-demand preview and opened settings", async () => {
  const f = fixture();
  render(
    <QueryClientProvider client={f.client}>
      <OrdersConfig
        scope={scope}
        config={config}
        onSaved={() => {}}
        onDirty={() => {}}
      />
      <MetadataProbe />
    </QueryClientProvider>,
  );
  await waitFor(() => expect(f.attempts).toHaveLength(1));
  expect(f.attempts).toHaveLength(1);
  await act(async () => {
    f.attempts[0].resolve(json(metadata));
  });
  await screen.findAllByText("Своя организация");
  expect(f.attempts).toHaveLength(1);
});

it("does not expose another account's cached references and rejects mismatched source scope without retries", async () => {
  const f = fixture();
  f.client.setQueryData(["orders-metadata", scope], metadata);
  const next = { ...scope, ozon: "other" };
  const mounted = render(
    <QueryClientProvider client={f.client}>
      <MetadataProbe currentScope={next} enabled={false} />
    </QueryClientProvider>,
  );
  expect(screen.queryByText("Своя организация")).toBeNull();
  expect(f.attempts).toHaveLength(0);
  mounted.rerender(
    <QueryClientProvider client={f.client}>
      <MetadataProbe currentScope={next} />
    </QueryClientProvider>,
  );
  await waitFor(() => expect(f.attempts).toHaveLength(1));
  await act(async () => {
    f.attempts[0].resolve(json(metadata));
  });
  await screen.findByText("Ошибка справочников");
  expect(f.client.getQueryData(["orders-metadata", next])).toBeUndefined();
  expect(f.attempts).toHaveLength(1);
});

it("preserves API authorization errors without retries or exposing cached metadata", async () => {
  const f = fixture();
  render(
    <QueryClientProvider client={f.client}>
      <MetadataProbe />
    </QueryClientProvider>,
  );
  await waitFor(() => expect(f.attempts).toHaveLength(1));
  await act(async () => {
    f.attempts[0].resolve(
      new Response(JSON.stringify({ error: "UNAUTHORIZED" }), { status: 401 }),
    );
  });
  await screen.findByText("Ошибка справочников");
  expect(f.client.getQueryData(["orders-metadata", scope])).toBeUndefined();
  expect(f.attempts).toHaveLength(1);
});
