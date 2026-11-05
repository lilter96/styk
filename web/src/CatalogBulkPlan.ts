import { newCatalogCreation } from "./CatalogSyncCreation";
import {
  type CatalogConfig,
  type CatalogProduct,
  type CatalogWork,
  type Enrollment,
  type Identity,
  type Scope,
} from "./CatalogSyncTypes";

const same = (a: Identity, b: Identity) =>
  a.id === b.id &&
  a.account.id === b.account.id &&
  a.account.provider === b.account.provider;
const beforeDispatch = new Set([
  "CONFIG_MAPPING_OR_CREDENTIALS_CHANGED",
  "SCOPED_CREDENTIALS_UNAVAILABLE",
  "SOURCE_OR_TARGET_CHANGED",
  "INTENT_BINDING_REJECTED",
  "FINAL_GUARD_CHANGED",
]);
export type BulkPlan = {
  configRevision: number;
  targets: Enrollment[];
  pending: Enrollment[];
  products: CatalogProduct[];
  archived: number;
  existing: number;
  unresolved: number;
};

export function bulkImportPlan(
  scope: Scope,
  config: CatalogConfig,
  products: CatalogProduct[],
  enrollments: Enrollment[],
  works: CatalogWork[],
): BulkPlan {
  const result: BulkPlan = {
    configRevision: config.revision,
    targets: [...config.targets],
    pending: [],
    products: [],
    archived: 0,
    existing: 0,
    unresolved: 0,
  };
  const seen = new Set<string>();
  for (const product of products) {
    if (
      product.identity.account.provider !== "ozon" ||
      product.identity.account.id !== scope.ozon ||
      !product.identity.id
    )
      throw new Error("foreign catalog product");
    if (seen.has(product.identity.id)) continue;
    seen.add(product.identity.id);
    if (product.lifecycle === "archived") {
      result.archived++;
    }
    const history = works.filter((work) =>
      same(work.decision.source.identity, product.identity),
    );
    const candidate = [...enrollments, ...config.targets].find(
      (target) =>
        same(target.source, product.identity) ||
        same(target.target, product.identity),
    );
    if (
      candidate?.mapping_id ||
      history.some((work) => work.state === "RECONCILED")
    ) {
      result.existing++;
      continue;
    }
    if (
      history.some(
        (work) =>
          work.created_target ||
          work.state !== "BLOCKED" ||
          !beforeDispatch.has(work.code || ""),
      )
    ) {
      result.unresolved++;
      continue;
    }
    const target = candidate?.create
      ? candidate
      : newCatalogCreation(product, scope);
    if (
      !same(target.source, product.identity) ||
      target.target.account.provider !== "moysklad" ||
      target.target.account.id !== scope.moy ||
      !target.create
    )
      throw new Error("foreign creation target");
    if (target.target.entity_type !== "product" || target.create.parent)
      throw new Error("unsupported bulk creation");
    const values = Object.fromEntries(
      Object.entries(target.create.values).filter(([field]) =>
        ["name", "description", "article"].includes(field),
      ),
    );
    for (const field of ["name", "description"]) {
      const value = product.fields[field];
      if (
        value?.state === "known" &&
        typeof value.data === "string" &&
        value.data.trim()
      )
        values[field] = value;
    }
    if (product.identity.offer_id)
      values.article = { state: "known", data: product.identity.offer_id };
    if (product.lifecycle === "archived")
      values.lifecycle = { state: "known", data: "archived" };
    const enrolled = {
      ...target,
      create: {
        id: target.create.id,
        external_code: target.create.external_code,
        values,
      },
      automatic: false,
      automatic_fields: [],
    };
    result.targets = result.targets.filter((item) => item.id !== enrolled.id);
    result.targets.push(enrolled);
    result.pending.push(enrolled);
    result.products.push(product);
  }
  if (result.targets.length > 100) throw new Error("bulk enrollment limit");
  return result;
}
