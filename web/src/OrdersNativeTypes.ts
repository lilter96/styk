import type {
  OrderField,
  OrderLine,
  OrderWork,
  OrdersOperationPage,
  NativeScope,
} from "./OrdersTypes";
export type NativeDocumentPolicy = {
  enabled: boolean;
  allow_posted: boolean;
  actions: ("demand_create" | "salesreturn_create")[];
  maximum_quantity: string;
  event_max_age_seconds: number;
  attested_at: string;
  valid_until: string;
};
export type OrderNativeProposal = {
  posting_number: string;
  action: "demand_create" | "salesreturn_create";
  applicable: OrderField<boolean>;
  lines: { identity: OrderLine["identity"]; quantity: string }[];
  demand_work_id?: string;
  event_kind: "draft_plan" | "shipment_dispatched" | "return_received";
  event_occurred_at?: string;
  event_confirmed: boolean;
  planned_moment?: string;
  event_reference: string;
};
export type NativeDemandWork = OrderWork & {
  native_document?: { id: string; kind: string; observed_at: string };
  scope: NativeScope;
  command: OrderWork["command"] & {
    scope: NativeScope;
    native_movement?: {
      applicable: OrderField<boolean>;
      correlation: { kind: string };
    };
  };
};
export type NativeOperationPage = Omit<OrdersOperationPage, "operations"> & {
  operations: (Omit<OrdersOperationPage["operations"][number], "targets"> & {
    targets: NativeDemandWork[];
  })[];
};
