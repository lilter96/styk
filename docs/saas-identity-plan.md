# Merchant identity and existing-data migration

Each verified user owns one STYK merchant account. Registration creates the
internal account in the same transaction; the seller supplies only an email
address and sets a password after verifying it. Sessions are revocable,
HttpOnly and protected by CSRF checks. Multiple MoySklad or Ozon connections
may belong to that account. Registration does not grant live publication
permission.

The existing `saas_workspaces` and `saas_memberships` tables are retained as
internal storage for historical scope, audit records and existing foreign
keys. `saas_merchant_accounts` binds one user to one tenant, with uniqueness
in both directions. Merchant requests resolve that binding on the server;
existing domain endpoints accept an omitted tenant, and a supplied tenant must
match the authenticated account. Historic shared memberships do not grant
merchant access. Database-side mutation checks lock and recheck the binding,
session and owner before committing. The legacy member-management HTTP routes
and user-facing workspace controls are not exposed.

Migration 39 automatically binds only unambiguous existing owners. A user with
multiple owned tenants, or a tenant with multiple owners, remains unbound until
an operator resolves the ownership deliberately; no data or audit rows are
deleted. On the loopback database host, an operator may bind an already claimed
legacy tenant to its verified sole owner using:

```
STYK_LOCAL_CLAIM_ENABLE=true go run ./cmd/claim-workspace -tenant <tenant> -user-id <verified-user-id> -bind-only
```

For an unclaimed pre-identity tenant, omit `-bind-only`. This is a local
migration command, not a merchant onboarding step or HTTP endpoint. The
command verifies historical scope references and refuses to replace a user's
current account if it contains tenant-scoped business records. It records an
audit event and revokes existing sessions. Resolve ambiguous or nonempty cases
with a reviewed migration and backup rather than guessing or discarding data.
Migration rollback refuses to discard existing merchant bindings.

Provider credentials retain their original tenant identifiers and associated
encryption data. Back up the PostgreSQL database and the existing
`STYK_CREDENTIAL_ENCRYPTION_KEY` separately; losing or blindly replacing the
key makes stored credentials unavailable. Automatic key rotation is not yet
implemented. Use an HTTPS origin except for loopback development; a local
frontend proxy must preserve the browser's Host header. SMTP must support
STARTTLS. The API binds to loopback behind a trusted TLS proxy; configure
`STYK_TRUSTED_PROXY_CIDRS` only for proxies that sanitize client addresses.

Publication remains default-off for new merchants. The existing tightly scoped,
owner-authorized test-account policy is separate from production eligibility;
a production activation and security review are still required.
