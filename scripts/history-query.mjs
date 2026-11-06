import { createRealtime, rawSql } from "@neon/realtime/server";

// Parameters are supplied by the Go API only after its normal session and
// tenant authorization. Join live identity rows so revocation removes results.
export function historyQuery({ tenant, moy, ozon, user, audit }) {
  for (const value of [tenant, moy, ozon, user, audit]) {
    if (typeof value !== "string" || !value || value.length > 512)
      throw new Error("Invalid history scope");
  }
  return rawSql(`SELECT w.id, w.state, w.updated_at FROM orders_work w
    JOIN saas_merchant_accounts m ON m.tenant_id=w.tenant_id
    JOIN saas_users u ON u.id=m.user_id
    JOIN saas_sessions s ON s.user_id=u.id
    WHERE w.tenant_id=$1 AND w.moysklad_account=$2 AND w.ozon_account=$3
      AND u.id=$4 AND s.audit_id=$5 AND s.revoked_at IS NULL
      AND u.disabled_at IS NULL AND u.email_verified_at IS NOT NULL
      AND s.user_auth_version=u.auth_version
    ORDER BY w.updated_at DESC, w.id LIMIT 100`, [tenant, moy, ozon, user, audit]);
}

export function realtimeServer() {
  return createRealtime({
    secret: process.env.NEON_REALTIME_SECRET,
    db: new URL(process.env.DATABASE_URL_UNPOOLED || process.env.DATABASE_URL).pathname.slice(1),
  });
}
