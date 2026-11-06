import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, post, params, ApiError } from "./api";
import type { Confirmation } from "./Confirmation";
type Scope = { tenant: string; moy: string; ozon: string };
export type Evidence = {
  id: string;
  name: string;
  account: string;
  status: string;
  is_rfbs: boolean | null;
  observed_at: string;
  stock_rows: number;
};
type Mapping = {
  mapping_id: number;
  version: number;
  moysklad_warehouse_id: string;
  ozon_warehouse_id: string;
  provenance: string;
  confirmed_at: string;
};
type Review = {
  status: string;
  moysklad: Evidence | null;
  ozon: Evidence | null;
  mappings: Mapping[];
  history: Mapping[];
  has_next: boolean;
  offset: number;
};
export type DurableReview = {
  review: {
    id: string;
    tenant: string;
    moysklad_account: string;
    ozon_account: string;
    moysklad_warehouse_id: string;
    ozon_warehouse_id: string;
    moysklad_import_id: number;
    ozon_import_id: number;
    created_at: string;
    expires_at: string;
    existing_mapping_id: number | null;
    existing_mapping_version: number | null;
  };
  status: string;
  principal_type: string;
};
export function useWarehouseReview(
  scope: Scope,
  moy: string,
  ozon: string,
  active: Confirmation,
) {
  const client = useQueryClient();
  const [offset, setOffset] = useState(0);
  const [reviewID, setReviewID] = useState("");
  const [creating, setCreating] = useState(false);
  const [error, setError] = useState("");
  const [ack, setAck] = useState(false);
  const [explanation, setExplanation] = useState("");
  const q = useQuery({
    queryKey: ["warehouse-review", scope, moy, ozon, offset],
    queryFn: ({ signal }) =>
      api<Review>(
        "/api/warehouse-mapping-review?" +
          params({
            tenant: scope.tenant,
            moysklad_account: scope.moy,
            ozon_account: scope.ozon,
            moysklad_warehouse: moy,
            ozon_warehouse: ozon,
            offset,
          }),
        signal,
      ),
    retry: false,
  });
  const durable = useQuery({
    queryKey: ["warehouse-durable-review", reviewID],
    queryFn: ({ signal }) =>
      api<DurableReview>(`/api/warehouse-mapping-reviews/${reviewID}`, signal),
    enabled: !!reviewID,
    staleTime: 0,
    refetchInterval: 5000,
    retry: false,
  });
  useEffect(() => {
    if (
      [durable.error, q.error].some(
        (error) => error instanceof ApiError && error.status === 401,
      )
    )
      void client.invalidateQueries({ queryKey: ["session"] });
  }, [durable.error, q.error, client]);
  const eligible =
    q.data?.status === "ELIGIBLE_FOR_REVIEW" &&
    q.data.moysklad?.id === moy &&
    q.data.moysklad.account === scope.moy &&
    q.data.ozon?.id === ozon &&
    q.data.ozon.account === scope.ozon &&
    typeof q.data.ozon.is_rfbs === "boolean";
  const reviewed = durable.data?.review;
  const [clock, setClock] = useState(Date.now());
  useEffect(() => {
    const expires = reviewed ? Date.parse(reviewed.expires_at) : NaN;
    if (!Number.isFinite(expires)) return;
    const timer = window.setTimeout(
      () => {
        setClock(Date.now());
        setAck(false);
      },
      Math.max(0, Math.min(2147483647, expires - Date.now())),
    );
    return () => window.clearTimeout(timer);
  }, [reviewed]);
  const reviewMatchesSelection =
    reviewed?.id === reviewID &&
    reviewed.tenant === scope.tenant &&
    reviewed.moysklad_account === scope.moy &&
    reviewed.ozon_account === scope.ozon &&
    reviewed.moysklad_warehouse_id === moy &&
    reviewed.ozon_warehouse_id === ozon &&
    reviewed.moysklad_import_id > 0 &&
    reviewed.ozon_import_id > 0 &&
    ["LOCAL_OPERATOR_SESSION", "MERCHANT_SESSION"].includes(
      durable.data?.principal_type || "",
    );
  async function createReview() {
    if (creating || active.pending || !eligible) return;
    setCreating(true);
    setAck(false);
    setError("");
    try {
      const result = await post<DurableReview>(
        "/api/warehouse-mapping-reviews",
        {
          tenant: scope.tenant,
          moysklad_account: scope.moy,
          ozon_account: scope.ozon,
          moysklad_warehouse_id: moy,
          ozon_warehouse_id: ozon,
        },
      );
      setReviewID(result.review.id);
      setExplanation("");
      setAck(false);
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) {
        void client.invalidateQueries({ queryKey: ["session"] });
        setError("Сессия завершилась. Войдите снова.");
      } else if (
        e instanceof ApiError &&
        e.code === "WAREHOUSE_ALREADY_ASSIGNED"
      ) {
        setError(
          "Склад уже сопоставлен. Обновите выбор. История за пределами доступных аккаунтов не показывается.",
        );
        void client.invalidateQueries({ queryKey: ["warehouse-review"] });
      } else
        setError(
          "Не удалось проверить пару складов. Обновите данные и проверьте выбранные аккаунты.",
        );
    } finally {
      setCreating(false);
    }
  }

  const samePending =
    active.pending?.intent.kind === "warehouse" &&
    active.pending.scope.tenant === scope.tenant &&
    active.pending.scope.moy === scope.moy &&
    active.pending.scope.ozon === scope.ozon &&
    active.pending.product === `${moy} → ${ozon}`;
  const saved = !!reviewID && active.result?.review_id === reviewID;
  const canConfirm =
    ack &&
    !active.pending &&
    !creating &&
    !q.isError &&
    !durable.isError &&
    durable.data?.status === "CURRENT" &&
    reviewed?.existing_mapping_id === null &&
    active.result?.review_id !== reviewID &&
    active.failure?.reviewId !== reviewID &&
    Number.isFinite(Date.parse(reviewed.expires_at)) &&
    Math.max(clock, Date.now()) < Date.parse(reviewed.expires_at) &&
    eligible &&
    reviewMatchesSelection &&
    !q.isFetching &&
    !durable.isFetching;
  function confirm() {
    if (!canConfirm) return;
    try {
      active.start(
        scope,
        `${moy} → ${ozon}`,
        reviewID,
        explanation,
        "warehouse",
      );
    } catch {
      setError(
        "Комментарий содержит недопустимые символы или превышает 1000 байт UTF-8.",
      );
    }
  }
  return {
    q,
    offset,
    setOffset,
    durable,
    reviewID,
    creating,
    error,
    ack,
    setAck,
    explanation,
    setExplanation,
    eligible,
    reviewMatchesSelection,
    createReview,
    canConfirm,
    confirm,
    samePending,
    saved,
  };
}
