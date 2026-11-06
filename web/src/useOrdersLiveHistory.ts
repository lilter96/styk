import { useEffect, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import {
  createRealtimeClient,
  type SealedLiveQuery,
  type MaterializedLiveQuerySubscription,
} from "@neon/realtime/client";
import { api } from "./api";
import { ordersURL, type OrderScope } from "./OrdersTypes";

type HistoryRow = { id: string; state: string; updated_at: string };
type Capability = { query: SealedLiveQuery<HistoryRow>; user_id: string };
type Client = ReturnType<typeof createRealtimeClient>;
let shared: { user: string; client: Client } | undefined;

function clientFor(user: string): Client {
  if (shared?.user !== user) {
    shared?.client.close();
    shared = {
      user,
      client: createRealtimeClient({
        url: import.meta.env.VITE_NEON_REALTIME_URL,
      }),
    };
  }
  return shared!.client;
}

// The existing API still builds the history projection. Subscribe only to its
// scoped version signals, then refresh that projection through normal auth.
export function useOrdersLiveHistory(scope: OrderScope) {
  const cache = useQueryClient();
  const [status, setStatus] = useState("disabled");
  const { tenant, moy, ozon } = scope;
  useEffect(() => {
    if (!import.meta.env.VITE_NEON_REALTIME_URL || !tenant || !moy || !ozon)
      return;
    const controller = new AbortController();
    let stopped = false;
    let subscription: MaterializedLiveQuerySubscription<HistoryRow> | undefined;
    let renewal: ReturnType<typeof setTimeout> | undefined;
    let refresh: ReturnType<typeof setTimeout> | undefined;
    let connecting: ReturnType<typeof setTimeout> | undefined;
    const requestScope = { tenant, moy, ozon };
    const fetchQuery = () =>
      api<Capability>(
        ordersURL("history-live", requestScope),
        controller.signal,
      );
    function stop() {
      stopped = true;
      controller.abort();
      clearTimeout(renewal);
      clearTimeout(refresh);
      clearTimeout(connecting);
      subscription?.unsubscribe();
    }
    function failed() {
      if (stopped) return;
      setStatus("error");
      stop();
    }
    function schedule(capability: Capability) {
      renewal = setTimeout(
        () => {
          void fetchQuery()
            .then(async (next) => {
              if (stopped) return;
              if (next.user_id !== capability.user_id) {
                failed();
                return;
              }
              await subscription?.renew(next.query);
              if (!stopped) schedule(next);
            })
            .catch(failed);
        },
        Math.max(1000, capability.query.expiresAt - Date.now() - 10000),
      );
    }
    void fetchQuery()
      .then((capability) => {
        if (stopped) return;
        subscription = clientFor(capability.user_id).subscribe(
          capability.query,
        );
        setStatus(subscription.getState().status);
        connecting = setTimeout(failed, 20000);
        subscription.onChange((snapshot) => {
          if (stopped) return;
          if (snapshot.status === "live") clearTimeout(connecting);
          clearTimeout(refresh);
          refresh = setTimeout(() => {
            void cache.invalidateQueries({
              queryKey: ["orders-operations", requestScope],
            });
          }, 100);
        });
        subscription.onStateChange((state) => {
          if (!stopped) setStatus(state.status);
          if (state.status === "error") failed();
        });
        schedule(capability);
      })
      .catch(failed);
    return stop;
  }, [cache, tenant, moy, ozon]);
  return status;
}
