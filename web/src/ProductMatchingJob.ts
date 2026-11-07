import { api, ApiError, params, post } from "./api";
import { scopeBody, type Scope } from "./CatalogSyncTypes";
import { useQuery } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import {
  matchingCriteria,
  type MatchingCriterion,
  type MatchingJob,
} from "./ProductMatchingTypes";
export type { MatchingJob } from "./ProductMatchingTypes";
function stored<T>(key: string): T | null {
  try {
    return JSON.parse(localStorage.getItem(key) || "null") as T | null;
  } catch {
    return null;
  }
}
export function useProductMatchingJob(scope: Scope) {
  const storage = "styk.automatic-matching." + JSON.stringify(scope);
  const [id, setID] = useState(() => localStorage.getItem(storage) || "");
  const [criterion, setCriterionState] = useState<MatchingCriterion>(() => {
    const value = localStorage.getItem(storage + ".criterion");
    return (
      matchingCriteria.find((c) => c.id === value)?.id || "article_or_code"
    );
  });
  const [starting, setStarting] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [uncertain, setUncertain] = useState(
    () => !!stored(storage + ".approval"),
  );
  const query = useQuery<MatchingJob>({
    queryKey: ["matching-job", scope, id],
    enabled: !!id,
    retry: false,
    queryFn: ({ signal }) =>
      api(
        `/api/product-matching/jobs/${id}?` + params(scopeBody(scope)),
        signal,
      ),
    refetchInterval: (q) =>
      q.state.data &&
      q.state.data.state !== "QUEUED" &&
      q.state.data.state !== "RUNNING" &&
      !["QUEUED", "RUNNING"].includes(q.state.data.apply.state)
        ? false
        : 1500,
  });
  useEffect(() => {
    if (query.data?.apply && query.data.apply.state !== "NOT_STARTED") {
      localStorage.removeItem(storage + ".approval");
      setUncertain(false);
    }
  }, [query.data?.apply.state, storage]);
  async function start(signal: AbortSignal) {
    const saved = stored<{ key: string; criterion: MatchingCriterion }>(
      storage + ".intent",
    );
    const intent =
      saved?.criterion === criterion
        ? saved
        : { key: crypto.randomUUID(), criterion };
    localStorage.setItem(storage + ".intent", JSON.stringify(intent));
    setStarting(true);
    try {
      const value = await post<MatchingJob>(
        "/api/product-matching/jobs",
        { ...scopeBody(scope), criterion, idempotency_key: intent.key },
        signal,
      );
      if (!value.id || value.criterion !== criterion)
        throw new Error("Unexpected matching job");
      localStorage.removeItem(storage + ".intent");
      localStorage.setItem(storage, value.id);
      setID(value.id);
    } catch (error) {
      if (
        error instanceof ApiError &&
        [400, 403, 409, 422].includes(error.status)
      )
        localStorage.removeItem(storage + ".intent");
      throw error;
    } finally {
      setStarting(false);
    }
  }
  async function apply(signal: AbortSignal) {
    if (!query.data) return;
    const previous = stored<{ id: string; key: string; expected: number }>(
      storage + ".approval",
    );
    const intent =
      previous?.id === id
        ? previous
        : {
            id,
            key: crypto.randomUUID(),
            expected: query.data.summary.exact || 0,
          };
    localStorage.setItem(storage + ".approval", JSON.stringify(intent));
    setSubmitting(true);
    try {
      await post(
        `/api/product-matching/jobs/${id}/apply`,
        {
          ...scopeBody(scope),
          idempotency_key: intent.key,
          expected_count: intent.expected,
          acknowledge: true,
        },
        signal,
      );
      localStorage.removeItem(storage + ".approval");
      setUncertain(false);
      await query.refetch();
    } catch (error) {
      const unknown =
        uncertain || !(error instanceof ApiError) || error.status >= 500;
      if (!unknown) localStorage.removeItem(storage + ".approval");
      setUncertain(unknown);
      throw error;
    } finally {
      setSubmitting(false);
    }
  }
  return {
    id,
    criterion,
    query,
    starting,
    submitting,
    uncertain,
    start,
    apply,
    setCriterion: (value: MatchingCriterion) => {
      localStorage.setItem(storage + ".criterion", value);
      setCriterionState(value);
    },
    resume: async () => {
      await post(`/api/product-matching/jobs/${id}/retry`, scopeBody(scope));
      await query.refetch();
    },
  };
}
