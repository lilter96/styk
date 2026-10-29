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
