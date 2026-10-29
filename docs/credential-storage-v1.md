# Scoped credential storage V1

Provider keys are encrypted at rest with AES-256-GCM and a dedicated 32-byte key supplied as base64 `STYK_CREDENTIAL_ENCRYPTION_KEY` to the API and worker. The key is never generated, stored in PostgreSQL, reused from the operator authentication secret, or returned by the console. Missing configuration disables credential-saving controls while existing environment-only import workflows remain available. Invalid supplied configuration fails API and worker startup. Back up and distribute the encryption key separately from database backups; losing it makes stored credentials unavailable.

## HTTP contract

All endpoints use the existing authenticated local-operator session and same-origin JSON mutation checks. They do not introduce production tenant authorization. Never persist submitted secrets in browser storage or include them in logging/analytics/error reports.

`GET /api/integrations/{provider}/credentials?tenant=...&account=...` returns:

```json
{"storage_available":true,"credential":{"tenant":"...","provider":"moysklad","account":"...","name":"Основной склад","revision":1,"enabled":true,"updated_at":"...","checked_at":"...","last_request_id":"..."}}
```

`credential` is null when no stored record exists. Omitting account selects the most recently updated stored provider connection for that tenant, if any. No secret/ciphertext/digest is exposed.

`POST /api/integrations/{provider}/credentials` accepts exactly:

```json
{"tenant":"...","account":"...","name":"Основной склад","secret":"...","request_id":"client UUID","expected_revision":0,"acknowledge":true}
```

Ozon requires `account` (Client-ID). A new MoySklad connection may send an empty account: the server derives the account from the authenticated `/context/employee` response, then tests a bounded product read. For an existing MoySklad account, the returned identity must match the supplied account. The explicit acknowledgement covers adding the account to the selected workspace as well as storing its credential. Save/probe does not queue an import.

Names are trimmed, nonempty and at most 120 characters; secrets are nonempty, at most 8192 bytes, with no surrounding whitespace/newlines. Revision zero means create; updates require the exact current revision. A successful bounded probe precedes one transaction that records ownership (without adopting historical imports), encrypted material and a replay ledger. There is no database transaction across a provider request.

Response: `{"credential":{...metadata...},"idempotent_replay":false}`. Repeating the same request ID and exact content returns the original metadata with replay true before contacting the provider. Reusing the ID with different input is rejected. A replay does not restore a superseded key; refetch current metadata after recovery.

`POST /api/integrations/{provider}/disconnect` accepts `{tenant,account,request_id,expected_revision,acknowledge:true}`. It removes encrypted material and increments revision, retaining account registration, import/mapping history and the metadata/replay ledger. A disabled stored record overrides an environment fallback. Already dispatched network operations may finish; a new import/publication cannot resolve the disabled credential.

Errors are safe codes only:

- `CREDENTIAL_STORAGE_UNAVAILABLE` (503): master-key capability unavailable, no mutation attempted.
- `INVALID_REQUEST` / `INVALID_SCOPE` (400): input rejected.
- `INVALID_CREDENTIALS`, `PERMISSION_DENIED`, `ACCOUNT_IDENTITY_MISMATCH`, `PROBE_TIMEOUT`, `PROBE_FAILED` (422): probe rejected/interrupted; credential not committed.
- `IDEMPOTENCY_CONFLICT` / `CREDENTIAL_VERSION_CONFLICT` / `OWNERSHIP_CONFLICT` (409): reconcile metadata before another attempt.
- `CREDENTIAL_NOT_FOUND` (404).
- `CREDENTIAL_PERSISTENCE_UNKNOWN` (503): a write may have committed. Keep the original body/request ID in memory and retry it identically. Never infer failure and submit a fresh mutation.

## Cryptography and scope

GCM authenticated additional data contains a version marker plus immutable tenant/provider/account and credential revision. Nonces use crypto/rand. Ciphertext cannot be moved to a different scope or revision. Request digests use HMAC-SHA256 with a separately derived key, preventing database-only offline verification of guessed secrets. Current encrypted material is retained only for enabled connections; the replay ledger contains no key or secret-bearing request body.

Provider constructors use only official fixed hosts in production, with redirects disabled. Tests inject loopback clients without relaxing those production defaults. Environment fallback resolves only the exact configured provider account and its registered workspace. The worker resolves stored keys at job execution time; browser-provided identities never supply worker credentials.

MoySklad identity endpoint contract: [official JSON API documentation](https://dev.moysklad.ru/doc/api/remap/1.2/) documents `/context/employee` and mandatory response `accountId`.

## Verification

Vault unit tests verify nonce uniqueness, ciphertext/scope/revision authentication, keyed request digests, missing/invalid configuration, private-field serialization and owner-bound environment fallback. The new bounded MoySklad identity read has an allowlisted synthetic test. Dedicated local PostgreSQL tests cover key-only identity derivation, successful/failed probes, same-ID lost-response recovery before network access, content conflicts, stale/concurrent revisions, cross-workspace ownership, atomic registration rollback, origin rejection, immutable request history, a real shared-worker import using the stored key, disconnect/replay, encrypted-material removal, and disabled-key precedence even when the encryption key is missing. Already accepted import intents remain recoverable after disconnect; fresh requests are rejected.

Migration 23 was applied on fresh disposable local databases, rolled down to 22 while empty and applied again. Its Down migration refuses to discard any stored credential or immutable request history; a populated rollback attempt was rejected with both records and schema version 23 preserved. The credential integration cases pass with the Go race detector, including a scoped environment-credential probe before separate explicit registration. No live provider endpoint was contacted.

Run against the dedicated local database `styk_credentials_v1_test`, migrated through at least version 23:

```sh
TEST_DATABASE_URL='<dedicated local database URL>' go test -race -tags=integration -count=1 -run '^TestCredential' ./internal/console
```

Frontend credential tests cover explicit acknowledgement, password masking/clearing and absence from serialized HTML/storage, immutable retries after settings/scope changes, original-scope reconciliation, known rejection handling, reply scope/revision validation, blocked concurrent mutations, separate disconnect acknowledgement, key-only new-account creation, and unavailable-storage gating.
