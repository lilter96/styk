import { api, params, post } from "./api";
import { scopeBody, type Scope } from "./CatalogSyncTypes";
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import type { MatchingPreview } from "./ProductAutoMatching";
export type MatchingJob = {
  id: string;
  state: "QUEUED" | "RUNNING" | "COMPLETED" | "STALE" | "FAILED";
  processed: number;
  total: number;
  pages: number;
  code: string;
  summary?: Record<string, number>;
  preview?: MatchingPreview;
};
function savedJobIntent(key: string): { revision: number; key: string } | null {
  try {
    const value = JSON.parse(localStorage.getItem(key) || "null");
    return value &&
      Number.isSafeInteger(value.revision) &&
      typeof value.key === "string"
      ? value
      : null;
  } catch {
    return null;
  }
}
export function useProductMatchingJob(scope: Scope) {
  const storageKey = "styk.matching-job." + JSON.stringify(scope);
  const [id, setID] = useState(() => localStorage.getItem(storageKey) || "");
  const [page, setPageState] = useState(() =>
    Math.max(
      0,
      Math.floor(Number(localStorage.getItem(storageKey + ".page")) || 0),
    ),
  );
  const setPage = (value: number) => {
    localStorage.setItem(storageKey + ".page", String(value));
    setPageState(value);
  };
  const query = useQuery({
    queryKey: ["matching-job", scope, id, page],
    enabled: !!id,
    queryFn: ({ signal }) =>
      api<MatchingJob>(
        `/api/product-matching/jobs/${id}?` +
          params({ ...scopeBody(scope), page }),
        signal,
      ),
    retry: false,
    refetchInterval: (q) =>
      q.state.data &&
      ["COMPLETED", "STALE", "FAILED"].includes(q.state.data.state)
        ? false
        : 2000,
  });
  async function start(revision: number, signal: AbortSignal) {
    const pendingKey = storageKey + ".intent";
    const intent = savedJobIntent(pendingKey) || {
      revision,
      key: crypto.randomUUID(),
    };
    localStorage.setItem(pendingKey, JSON.stringify(intent));
    const value = await post<MatchingJob>(
      "/api/product-matching/jobs",
      {
        ...scopeBody(scope),
        expected_revision: intent.revision,
        idempotency_key: intent.key,
      },
      signal,
    );
    localStorage.removeItem(pendingKey);
    localStorage.setItem(storageKey, value.id);
    setID(value.id);
    setPage(0);
    return value;
  }
  return {
    id,
    page,
    query,
    start,
    setPage,
    clear: async () => {
      if (id)
        await post(`/api/product-matching/jobs/${id}/cancel`, scopeBody(scope));
      localStorage.removeItem(storageKey + ".intent");
      localStorage.removeItem(storageKey);
      localStorage.removeItem(storageKey + ".page");
      setID("");
      setPage(0);
    },
  };
}
