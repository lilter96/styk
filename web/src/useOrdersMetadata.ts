import { useQuery } from "@tanstack/react-query";
import { api } from "./api";
import {
  ordersURL,
  sameOrderScope,
  type OrderScope,
  type OrdersMetadata,
} from "./OrdersTypes";

// Settings and readable mirror previews share one scoped, cancellable read.
// Merely mounting the merchant order page must not traverse provider metadata.
export function useOrdersMetadata(scope: OrderScope, enabled: boolean) {
  return useQuery({
    queryKey: ["orders-metadata", scope],
    enabled,
    queryFn: async ({ signal }) => {
      const value = await api<OrdersMetadata>(
        ordersURL("metadata", scope),
        signal,
      );
      if (!sameOrderScope(value.scope, scope))
        throw new Error("metadata scope mismatch");
      return value;
    },
    retry: false,
    staleTime: 300000,
    refetchOnWindowFocus: false,
  });
}
