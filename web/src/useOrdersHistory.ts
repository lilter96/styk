import { useQuery } from "@tanstack/react-query";
import { api } from "./api";
import {
  ordersURL,
  sameOrderScope,
  type OrderScope,
  type OrdersOperationPage,
} from "./OrdersTypes";

function useHistoryPage(scope: OrderScope, cursor: string, enabled = true) {
  return useQuery({
    queryKey: ["orders-operations", scope, cursor],
    enabled,
    queryFn: async ({ signal }) => {
      const value = await api<OrdersOperationPage>(
        ordersURL("operations", scope, { cursor, limit: "25" }),
        signal,
      );
      if (
        (value.operations ?? []).some(
          (operation) =>
            !sameOrderScope(operation.scope, scope) ||
            (operation.targets ?? []).some(
              (work) => !sameOrderScope(work.command.scope, scope),
            ),
        )
      )
        throw new Error("operation scope mismatch");
      return value;
    },
    retry: false,
    refetchOnWindowFocus: false,
    refetchInterval: (query) =>
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
export function useOrdersHistory(scope: OrderScope, cursor: string) {
  const latest = useHistoryPage(scope, "");
  const older = useHistoryPage(scope, cursor, cursor !== "");
  return { latest, page: cursor === "" ? latest : older };
}
