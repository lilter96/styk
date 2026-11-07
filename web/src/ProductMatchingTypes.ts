export type MatchingCriterion = "article_or_code" | "article" | "code";
export const matchingCriteria: {
  id: MatchingCriterion;
  title: string;
  description: string;
}[] = [
  {
    id: "article_or_code",
    title: "Артикул или код",
    description: "Артикул Ozon совпадает с артикулом или кодом МойСклад",
  },
  {
    id: "article",
    title: "Только артикул",
    description: "Артикул Ozon совпадает с артикулом МойСклад",
  },
  {
    id: "code",
    title: "Только код",
    description: "Артикул Ozon совпадает с кодом МойСклад",
  },
];
export type MatchingCandidate = {
  matched_field: "article" | "code";
  matched_value: string;
  id: string;
  kind: string;
  name: string;
  article: string;
  evidence: string;
  score: number;
};
export type MatchItem = {
  product_id: string;
  offer_id: string;
  name: string;
  status: string;
  reason: string;
  candidates: MatchingCandidate[];
  shared_inventory: boolean;
};
export type MatchingPolicy = {
  revision: number;
  criterion?: MatchingCriterion;
  exact_sku: boolean;
  names: boolean;
  auto_confirm: boolean;
  after_import: boolean;
};
export type MatchingJob = {
  id: string;
  criterion: MatchingCriterion;
  state: "QUEUED" | "RUNNING" | "COMPLETED" | "STALE" | "FAILED";
  processed: number;
  total: number;
  code: string;
  summary: Record<string, number>;
  examples: MatchItem[];
  attention: MatchItem[];
  apply: {
    state:
      | "NOT_STARTED"
      | "QUEUED"
      | "RUNNING"
      | "COMPLETED"
      | "FAILED"
      | "STALE"
      | "CANCELLED";
    total: number;
    confirmed: number;
    skipped: number;
    code: string;
  };
};
