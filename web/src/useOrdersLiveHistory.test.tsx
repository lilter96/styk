import { afterEach, expect, it, vi } from "vitest";
import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { api } from "./api";
import { useOrdersLiveHistory } from "./useOrdersLiveHistory";

const sdk = vi.hoisted(() => ({
  change: undefined as undefined | ((snapshot: { status: string }) => void),
  state: undefined as undefined | ((state: { status: string }) => void),
  unsubscribe: vi.fn(),
  renew: vi.fn().mockResolvedValue(undefined),
  close: vi.fn(),
}));
vi.mock("./api", () => ({ api: vi.fn() }));
vi.mock("@neon/realtime/client", () => ({
  createRealtimeClient: () => ({
    close: sdk.close,
    subscribe: () => ({
      getState: () => ({ status: "connecting" }),
      onChange: (callback: typeof sdk.change) => {
        sdk.change = callback;
      },
      onStateChange: (callback: typeof sdk.state) => {
        sdk.state = callback;
      },
      unsubscribe: sdk.unsubscribe,
      renew: sdk.renew,
    }),
  }),
}));
afterEach(() => {
  cleanup();
  vi.unstubAllEnvs();
  vi.clearAllMocks();
});
const scope = { tenant: "live-tenant", moy: "moy", ozon: "ozon" };
function setup() {
  vi.stubEnv("VITE_NEON_REALTIME_URL", "wss://realtime.example.test");
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  return { client, wrapper };
}

it("refreshes the authorized history projection and releases its subscription", async () => {
  const { client, wrapper } = setup();
  vi.mocked(api).mockResolvedValue({
    user_id: "first-user",
    query: { expiresAt: Date.now() + 60000 },
  });
  const invalidate = vi.spyOn(client, "invalidateQueries");
  const view = renderHook(() => useOrdersLiveHistory(scope), { wrapper });
  await waitFor(() => expect(view.result.current).toBe("connecting"));
  act(() => {
    sdk.state?.({ status: "live" });
    sdk.change?.({ status: "live" });
  });
  await waitFor(() =>
    expect(invalidate).toHaveBeenCalledWith({
      queryKey: ["orders-operations", scope],
    }),
  );
  expect(view.result.current).toBe("live");
  view.unmount();
  expect(sdk.unsubscribe).toHaveBeenCalledOnce();
});

it("stops renewal when the authenticated user changes", async () => {
  const { wrapper } = setup();
  vi.mocked(api)
    .mockResolvedValueOnce({
      user_id: "second-user",
      query: { expiresAt: Date.now() + 11000 },
    })
    .mockResolvedValueOnce({
      user_id: "different-user",
      query: { expiresAt: Date.now() + 60000 },
    });
  const view = renderHook(() => useOrdersLiveHistory(scope), { wrapper });
  await waitFor(() => expect(view.result.current).toBe("error"), {
    timeout: 3000,
  });
  expect(sdk.renew).not.toHaveBeenCalled();
  expect(sdk.unsubscribe).toHaveBeenCalledOnce();
});
