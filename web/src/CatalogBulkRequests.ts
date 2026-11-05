import { api, ApiError, post } from "./api";
import { bulkImportPlan, type BulkPlan } from "./CatalogBulkPlan";
import {
  catalogURL,
  configBody,
  normalizeConfig,
  scopeBody,
  type CatalogConfig,
  type CatalogOperation,
  type CatalogPreview,
  type Enrollment,
  type OperationPage,
  type ProductPage,
  type Scope,
} from "./CatalogSyncTypes";

const timeout = (signal: AbortSignal) =>
  AbortSignal.any([signal, AbortSignal.timeout(45000)]);
const sameScope = (value: CatalogConfig["scope"], scope: Scope) =>
  JSON.stringify(scopeBody(scope)) ===
  JSON.stringify({
    tenant: value?.tenant,
    moysklad_account: value?.moysklad_account,
    ozon_account: value?.ozon_account,
  });

async function pages<T extends { next_cursor?: string }>(
  read: (cursor: string) => Promise<T>,
): Promise<T[]> {
  const result: T[] = [];
  const seen = new Set<string>();
  let cursor = "";
  for (let page = 0; page < 20; page++) {
    const value = await read(cursor);
    result.push(value);
    if (!value.next_cursor) return result;
    if (seen.has(value.next_cursor)) throw new Error("repeated catalog cursor");
    cursor = value.next_cursor;
    seen.add(cursor);
  }
  throw new Error("catalog traversal limit");
}

export async function prepareBulkImport(
  scope: Scope,
  config: CatalogConfig,
  signal: AbortSignal,
): Promise<BulkPlan> {
  const [products, history] = await Promise.all([
    pages((cursor) =>
      api<ProductPage>(
        catalogURL("products", scope, {
          provider: "ozon",
          limit: "100",
          cursor,
        }),
        timeout(signal),
      ),
    ),
    pages((cursor) =>
      api<OperationPage>(
        catalogURL("operations", scope, { limit: "100", cursor }),
        timeout(signal),
      ),
    ),
  ]);
  if (
    history.some((page) =>
      page.operations.some((operation) => !sameScope(operation.scope, scope)),
    )
  )
    throw new Error("foreign catalog operation");
  return bulkImportPlan(
    scope,
    config,
    products.flatMap((page) => page.products || []),
    products.flatMap((page) => page.enrollments || []),
    history.flatMap((page) =>
      page.operations.flatMap((operation) => operation.targets || []),
    ),
  );
}

export type BulkResult = {
  queued: number;
  failed: string[];
  uncertain: boolean;
};
export async function runBulkImport(
  scope: Scope,
  config: CatalogConfig,
  plan: BulkPlan,
  signal: AbortSignal,
  saved: (config: CatalogConfig) => void,
  progress: (count: number) => void,
): Promise<BulkResult> {
  const run = crypto.randomUUID();
  signal.throwIfAborted();
  if (plan.configRevision !== config.revision)
    throw new Error("catalog configuration changed");
  const current = normalizeConfig(
    await post<CatalogConfig>(
      "/api/catalog-sync/config",
      {
        ...scopeBody(scope),
        config: configBody({
          ...config,
          targets: plan.targets,
          enabled: true,
          test_enabled: true,
        }),
        expected_revision: config.revision,
        idempotency_key: `${run}:config`,
      },
      timeout(signal),
    ),
  );
  if (!sameScope(current.scope, scope))
    throw new Error("foreign catalog configuration");
  saved(current);
  const result: BulkResult = { queued: 0, failed: [], uncertain: false };
  for (const target of plan.pending) {
    signal.throwIfAborted();
    const preview = await previewOne(scope, current, target, signal);
    if (!preview) {
      result.failed.push(target.id);
      progress(result.queued + result.failed.length);
      continue;
    }
    try {
      signal.throwIfAborted();
      const operation = await post<CatalogOperation>(
        "/api/catalog-sync/approve",
        {
          ...scopeBody(scope),
          preview_id: preview.id,
          enrollment_ids: [target.id],
          acknowledge: true,
          idempotency_key: `${run}:${target.id}`,
        },
        timeout(signal),
      );
      if (
        !operation.id ||
        !sameScope(operation.scope, scope) ||
        operation.targets.length !== 1 ||
        operation.targets[0].enrollment_id !== target.id
      )
        throw new Error("unconfirmed catalog acknowledgement");
      result.queued++;
    } catch (error) {
      if (
        error instanceof ApiError &&
        [400, 403, 409, 410, 422].includes(error.status)
      )
        result.failed.push(target.id);
      else {
        result.uncertain = true;
        break;
      }
    }
    progress(result.queued + result.failed.length);
  }
  return result;
}

async function previewOne(
  scope: Scope,
  config: CatalogConfig,
  target: Enrollment,
  signal: AbortSignal,
) {
  const preview = await post<CatalogPreview>(
    "/api/catalog-sync/preview",
    {
      ...scopeBody(scope),
      expected_revision: config.revision,
      enrollment_ids: [target.id],
      manual_values: {},
    },
    timeout(signal),
  );
  if (
    !sameScope(preview.scope, scope) ||
    preview.config_revision !== config.revision ||
    preview.decisions.length !== 1 ||
    preview.decisions[0].enrollment_id !== target.id
  )
    throw new Error("foreign catalog preview");
  const decision = preview.decisions[0];
  if (
    !decision.ready ||
    decision.validation?.length ||
    decision.plan?.conflicts?.length ||
    decision.commands.length !== 1 ||
    decision.commands[0].action !== "create"
  )
    return null;
  return preview;
}
