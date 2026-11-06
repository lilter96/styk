import { useQuery } from "@tanstack/react-query";
import { api } from "./api";
import { useOrdersLiveHistory } from "./useOrdersLiveHistory";
import {
  ordersURL,
  sameOrderScope,
  type OrderScope,
  type OrdersOperationPage,
} from "./OrdersTypes";

function useHistoryPage(
  scope: OrderScope,
  cursor: string,
  enabled = true,
  search = "",
  status = "",
  attention = false,
  fallbackPolling = false,
) {
  return useQuery({
    queryKey:
      search || status || attention
        ? ["orders-operations", scope, cursor, search, status, attention]
        : ["orders-operations", scope, cursor],
    enabled,
    queryFn: async ({ signal }) => {
      const value = await api<OrdersOperationPage>(
        ordersURL("operations", scope, {
          cursor,
          limit: "25",
          search,
          status,
          attention: String(attention),
        }),
        signal,
      );
      if (
        (value.operations ?? []).some(
          (operation) =>
            !sameOrderScope(operation.scope, scope) ||
            (operation.targets ?? []).some(
              (work) =>
                !sameOrderScope(work.scope, scope) ||
                !sameOrderScope(work.command.scope, scope),
            ),
        )
      )
        throw new Error("operation scope mismatch");
      return value;
    },
    retry: false,
    refetchOnWindowFocus: false,
    refetchInterval: (query) =>
      fallbackPolling ||
      (query.state.data?.operations ?? []).some((operation) =>
        (operation.targets ?? []).some((work) =>
          [
            "QUEUED",
            "CLAIMED",
            "DISPATCHING",
            "ACKNOWLEDGED",
            "POLLING",
            "UNKNOWN",
          ].includes(work.state),
        ),
      )
        ? 10000
        : false,
  });
}

// Browsing immutable history must not replace the latest operation context.
// The first page stays subscribed while the operator reads an older page.
export function useOrdersHistory(
  scope: OrderScope,
  cursor: string,
  search = "",
  status = "",
  attention = false,
) {
  // Latest outcomes protect posting actions even while a filtered/older page
  // is displayed. Never replace this subscription with a filtered result.
  const liveStatus = useOrdersLiveHistory(scope);
  const latest = useHistoryPage(
    scope,
    "",
    true,
    "",
    "",
    false,
    liveStatus === "error",
  );
  const filtered = !!(cursor || search || status || attention);
  const page = useHistoryPage(
    scope,
    cursor,
    filtered,
    search,
    status,
    attention,
    liveStatus === "error",
  );
  return { latest, page: filtered ? page : latest, liveStatus };
}
