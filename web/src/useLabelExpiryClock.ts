import { useEffect, useState } from "react";
import type { OrdersOperationPage } from "./OrdersTypes";

// Reconciled operations no longer poll, but their private downloads still expire.
export function useLabelExpiryClock(page: OrdersOperationPage | undefined) {
  const [tick, setTick] = useState(Date.now);
  useEffect(() => {
    const now = Date.now();
    const deadlines = (page?.operations ?? []).flatMap((operation) =>
      (operation.targets ?? []).flatMap((work) => {
        const artifact = work.receipt?.artifact;
        if (
          work.state !== "RECONCILED" ||
          !artifact?.available ||
          !artifact.expires_at
        )
          return [];
        const deadline = Date.parse(artifact.expires_at);
        return Number.isFinite(deadline) && deadline > now ? [deadline] : [];
      }),
    );
    if (!deadlines.length) return;
    const delay = Math.min(Math.min(...deadlines) - now + 1, 2147483647);
    const timer = window.setTimeout(() => setTick(Date.now()), delay);
    return () => window.clearTimeout(timer);
  }, [page, tick]);
  return Date.now();
}
