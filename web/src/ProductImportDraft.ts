import { useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError, params, post } from "./api";
import {
  scopeBody,
  type Scope,
  type Provider,
  type CatalogProduct,
  type Enrollment,
  type ProductFacts,
  type CatalogPreview,
  type CatalogOperation,
} from "./CatalogSyncTypes";
export type ImportSelection = {
  product: CatalogProduct;
  enrollment: Enrollment;
  facts?: ProductFacts;
};
export type ImportIntent = {
  preview_id: string;
  enrollment_ids: string[];
  idempotency_key: string;
  operation?: CatalogOperation;
  attempted?: boolean;
  rejected?: string;
};
export type ImportDraftState = {
  selected: Record<string, ImportSelection>;
  plans: CatalogPreview[];
  intents: ImportIntent[];
};
type Draft = {
  revision: number;
  state: Partial<ImportDraftState>;
  updated_at: string;
};
export function useProductImportDraft(scope: Scope, provider: Provider) {
  const client = useQueryClient();
  const key = ["product-import-draft", scope, provider];
  const query = useQuery({
    queryKey: key,
    queryFn: ({ signal }) =>
      api<Draft>(
        "/api/product-import-draft?" +
          params({ ...scopeBody(scope), provider }),
        signal,
      ),
    retry: false,
  });
  const revision = useRef<number | null>(null);
  const pending = useRef<{ state: ImportDraftState; body: unknown } | null>(
    null,
  );
  const [saving, setSaving] = useState(false);
  const [message, setMessage] = useState("");
  const [blocked, setBlocked] = useState(false);
  async function save(state: ImportDraftState) {
    if (blocked)
      throw new Error(
        "Черновик изменён в другой вкладке. Перезагрузите страницу и восстановите сохранённую версию.",
      );
    if (!query.data || query.isError)
      throw new Error("Сначала загрузите сохранённый черновик.");
    if (
      pending.current &&
      JSON.stringify(pending.current.state) !== JSON.stringify(state)
    )
      throw new Error(
        "Результат сохранения неизвестен. Сначала повторите сохранение прежней версии.",
      );
    if (
      new TextEncoder().encode(JSON.stringify(state)).length >
      7 * 1024 * 1024
    )
      throw new Error(
        "Черновик превышает допустимый размер. Уменьшите выбор товаров или объём заполненных данных.",
      );
    if (!pending.current) {
      const snapshot = JSON.parse(JSON.stringify(state)) as ImportDraftState;
      pending.current = {
        state: snapshot,
        body: {
          ...scopeBody(scope),
          provider,
          revision: revision.current ?? query.data.revision,
          state: snapshot,
          idempotency_key: crypto.randomUUID(),
        },
      };
    }
    setSaving(true);
    setMessage("");
    try {
      const value = await post<Draft>(
        "/api/product-import-draft",
        pending.current.body,
        AbortSignal.timeout(30000),
      );
      revision.current = value.revision;
      pending.current = null;
      client.setQueryData(key, value);
      setMessage(
        "Черновик сохранён на сервере. Его можно восстановить после возвращения на страницу.",
      );
      return value;
    } catch (error) {
      if (error instanceof ApiError && error.status === 409) {
        setBlocked(true);
        pending.current = null;
        throw new Error(
          "Черновик изменён в другой вкладке. Перезагрузите страницу перед продолжением.",
          { cause: error },
        );
      }
      if (
        error instanceof ApiError &&
        [400, 403, 413, 422].includes(error.status)
      )
        pending.current = null;
      throw error;
    } finally {
      setSaving(false);
    }
  }
  return {
    query,
    save,
    saving,
    message,
    blocked,
    retry: () =>
      pending.current ? save(pending.current.state) : Promise.resolve(null),
  };
}
export function importChunks<T>(items: T[], size = 20): T[][] {
  const chunks: T[][] = [];
  for (let i = 0; i < items.length; i += size)
    chunks.push(items.slice(i, i + size));
  return chunks;
}
export function combinedImportPreview(
  plans: CatalogPreview[],
): CatalogPreview | null {
  if (!plans.length) return null;
  return {
    ...plans[0],
    id: "batch-overview",
    expires_at: plans.reduce(
      (a, p) => (a < p.expires_at ? a : p.expires_at),
      plans[0].expires_at,
    ),
    decisions: plans.flatMap((p) => p.decisions),
  };
}
