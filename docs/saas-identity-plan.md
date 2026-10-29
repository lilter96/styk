# SaaS identity migration

The current console authenticates a single loopback operator and trusts the
tenant supplied by that operator. Its database already scopes many mappings,
credentials, imports and publication decisions by tenant and provider account,
but those scope fields are not an authorization boundary for multiple users.

1. Reserve existing tenant identifiers as unclaimed workspaces without assigning
   them to a user. Add users, memberships, revocable database-backed sessions and
   security events. An explicit local bootstrap operation must claim legacy data.
2. Authenticate requests and authorize the requested workspace and action on the
   server, including ID-only review routes. Preserve actor identity in durable
   records; never trust a browser-provided account or tenant as proof of access.
3. Recheck account ownership and task scope at worker claims and credential
   resolution. Keep the existing outbox, publication fences and test-account
   allowlist; registration does not grant publication rights.
4. Replace free-text workspace selection with membership-backed navigation and
   clear tenant-specific browser state on identity or workspace changes.
5. Verify actual HTTP requests, database constraints, worker execution and two
   browser sessions with independent synthetic providers before remote rollout.

PostgreSQL RLS alone would not secure the current deployment: the application
uses pooled connections and the worker needs cross-tenant scheduling privileges;
table owners and privileged roles may bypass policies. Do not enable decorative
RLS without separate roles, transaction-scoped context and isolation tests.

This plan is not a production-readiness claim. Existing publication limits and
UNKNOWN handling remain in force during migration.

## Local migration and operation

Apply the ordinary migrations before starting merchant mode. Existing workspace
identifiers and historical records remain in place and are marked unclaimed;
registration does not give a new user access to them. Configure
`STYK_CREDENTIAL_ENCRYPTION_KEY`, `STYK_CONSOLE_ORIGIN`, `STYK_SMTP_ADDRESS`
and `STYK_SMTP_FROM`; authenticated SMTP additionally needs
`STYK_SMTP_USERNAME` and `STYK_SMTP_PASSWORD`. SMTP must support STARTTLS.
Use an HTTPS origin except for loopback development. The API binds to loopback
behind a trusted TLS proxy; configure `STYK_TRUSTED_PROXY_CIDRS` only for
proxies that sanitize and append the client address. Do not publish the API
listener directly. Merchant mode is the default; the old operator mode must be
explicitly selected with `STYK_AUTH_MODE=local` and remains loopback-only.
Email verification requires the mailbox holder to set the login password; it
replaces any password entered before address ownership was established.

To claim historical development data, first register and verify the intended
owner. On the database host, export the loopback `DATABASE_URL`, inspect the verified user ID locally and run
`STYK_LOCAL_CLAIM_ENABLE=true go run ./cmd/claim-workspace -tenant <tenant> -user-id <user-id>`
against a loopback PostgreSQL connection. The command rejects conflicting legacy
references and cannot be called through the HTTP API. Back up the database and
encryption key separately; loss of the key makes stored provider credentials
unrecoverable. Credential replacement is supported, but automated encryption
key rotation is not yet implemented.

New workspaces may store disabled synchronization drafts and import data, but
cannot enable real publication. The existing fixed test-account allowlist and
bounded publication policy remain the only live exception. A merchant
production-activation workflow, independent worker database roles and a
production deployment/security review remain outstanding. Rollback migrations
refuse to delete identity, audit or merchant-draft history; restore a verified
backup instead of forcing a destructive downgrade.

The local development database was migrated from version 32 to 37 on
2026-10-29. Counts before and after were unchanged: 2 connections, 4 product
mappings, 1 warehouse mapping, 33 source imports, 20 publications and 1
recovery. Automatic synchronization was disabled. The existing `cross-e2e`
workspace remains unclaimed with no assigned members; its owner must complete
the explicit claim after registering and verifying an address. A protected
local pre-migration backup was made outside version control. No provider write
was performed as part of this identity migration.
