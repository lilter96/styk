import type { NativeScope, OrderField } from "./OrdersTypes";

export type ExemplarMarkType = "mandatory_mark" | "jw_uin" | "imei";
export type ExemplarInput = {
  id: string;
  gtd?: OrderField<string>;
  gtd_absent?: OrderField<boolean>;
  rnpt?: OrderField<string>;
  rnpt_absent?: OrderField<boolean>;
  marks?: OrderField<{ type: ExemplarMarkType; value: string }[]>;
  clear_mark_types?: ExemplarMarkType[];
};
export type ExemplarSetInput = {
  products: { sku: string; exemplars: ExemplarInput[] }[];
};
export type ExemplarPublic = {
  scope: NativeScope;
  source_revision: string;
  work_id: string;
  proof_work_id?: string;
  proof_valid_until?: string;
  posting_number: string;
  target_fingerprint: string;
  products: {
    sku: string;
    quantity: number;
    required: Record<string, OrderField<boolean>>;
    ids: string[];
  }[];
  expires_at: string;
};
export type ExemplarDraft = {
  workID: string;
  input?: ExemplarSetInput;
  validUntil: string;
};
