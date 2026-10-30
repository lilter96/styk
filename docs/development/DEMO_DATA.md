# Isolated STYK development data

This fixture is optional development tooling. It is never called by API/worker startup and cannot authorize marketplace writes. Production migrations and synchronization rules are unchanged.

## Current local environment

- Real console: http://127.0.0.1:5173
- API: http://127.0.0.1:8080; probes `/health/live` and `/health/ready`.
- Mailpit: http://127.0.0.1:8025, STARTTLS SMTP on local port 1025.
- PostgreSQL: existing local `styk-redesign-db`, image `postgres:17.6`, loopback port 55433.
- Isolated database: `styk_demo_dev`; database comment must equal `STYK isolated development demo v1`.
- The former development database and provider credentials are preserved.

The lifecycle helper targets this explicitly verified Windows/Docker environment. It is not a generic production provisioner. On another machine, deliberately provision an equivalent isolated local instance or use the Go CLI against a marked local database; do not remove its guards to reach a remote database.

## Account and initial password

Email: `demo@styk.local`. Organization: `STYK Demo Company`. The actual role is `OWNER`, the administrator role supported by the current one-to-one merchant binding. The requested friendly label is STYK Demo Admin; the existing user schema has no display-name column, so no fabricated profile field was added.

The helper registers through `/api/registration`, reads only the matching synthetic email from local Mailpit, verifies it through `/api/verification`, and signs in through `/api/session`. It does not disable verification or insert a password hash manually.

The password is generated from 32 cryptographic random bytes. It is kept only in current-user Windows DPAPI ciphertext under ignored `.local/demo/`. No plaintext password is written to source, SQL, documentation, logs or Git. Retrieve it once in a trusted local terminal:

```powershell
python E:/work/styk/scripts/dev-demo.py password
```

This prints the initial password and removes the one-time encrypted delivery file. Runtime state retains encrypted credentials for authenticated checks. Use normal password recovery if local protected state is lost; do not weaken authentication.

## Data model and counts

| Dataset | Count / interpretation |
|---|---|
| Main owner and organization | 1 verified user; 1 merchant-bound workspace |
| Registered synthetic accounts | 1 MoySklad + 5 Ozon; no credentials |
| Physical product identities | 500, six category names reflected in descriptions |
| Ozon listing observations | 2,500: 500 for each account |
| Synthetic catalog prices | 2,500 operator-attested RUB snapshots; coherent across matching listings |
| Warehouses | 3 source + 15 marketplace warehouse observations |
| Stock rows | 1,500 source warehouse rows + 7,500 marketplace rows + 500 source aggregate rows |
| Product mappings/history | 1,960 each; remaining listings include reviewable ambiguity/unconfirmed cases |
| Warehouse mappings/history | 15 each |
| Orders | 1,000 Ozon postings + 1,000 corresponding MoySklad order observations and positions |
| Source order windows | Three 30-day windows covering the 90-day scenario |
| Import scenarios | 6 completed + 1 synthetic API failure + 1 cancelled/skipped + 2 waiting, including retry state |
| Safe worker tasks | 12 mock tasks; normal worker consumes them without provider calls |
| Total outbox events/tasks | 22 |

Physical items, listings and source/marketplace observations are different counts. These are not 2,500 distinct physical products or 2,000 unrelated customer purchases. No real buyer names, addresses, emails or phones are present.

The application does not implement a full order/return/fulfillment or financial analytics product. The seed therefore does not add production tables/APIs for returns, labels, revenue or profit. No cost-price column exists in the imported product model. Catalog prices are explicitly synthetic `OPERATOR_ATTESTED` facts, not successful provider observations or a new pricing module. Local product-image display/upload is not added solely for seeding.

WB and Yandex are not valid providers in the current connection schema. Their 3 + 1 demo accounts remain in the existing presentation demo; they are not fake working API connections. That layer already shows 3 WB + 5 Ozon + 1 Yandex + 1 MoySklad and is independent of database credentials.

## Seed and repeat

The reusable implementation is `cmd/demo-seed` → `internal/devseed`. SQL under `internal/devseed/sql` belongs to development fixtures, not Goose migrations. It performs no network calls to providers.

The Go command requires all of: `APP_ENV=development`, an exact loopback database URL for `styk_demo_dev`, no routing/session query overrides, a matching database comment, a verified demo owner, no other users, no credential rows and no non-demo external accounts. `-as-of` is mandatory; the Windows helper preserves its reference time in DPAPI state.

```powershell
python scripts/dev-demo.py seed
python scripts/dev-demo.py check
python scripts/dev-demo.py smoke
```

For Go-based isolated development tooling, export the local database configuration privately and run:

```sh
APP_ENV=development go run ./cmd/demo-seed -seed 42 -as-of REFERENCE_TIME_RFC3339
```

Business values are deterministic for seed, reference time and tenant. Authentication IDs, sequence IDs and audit clock timestamps are intentionally not claimed to be byte-identical across fresh registrations. Repeat execution takes an advisory transaction lock, checks its marker and counts, and returns `existing=true` without duplicate observations. Different parameters refuse replacement of immutable evidence.

All fixture groups and the marker commit in one transaction. Any SQL/constraint/count failure rolls everything back. A dedicated integration test injects a late constraint failure, checks zero leaked imports, then verifies concurrent idempotence and rejection of another user's database.

## Backup and guarded reset

Stop the known local demo processes before resetting. Do not use volume deletion or point this tooling at an existing customer database.

```powershell
python scripts/dev-demo.py stop
$env:APP_ENV = 'development'
python scripts/demo-db.py reset --confirm styk_demo_dev
python scripts/dev-demo.py start
python scripts/dev-demo.py enroll
python scripts/dev-demo.py seed
python scripts/dev-demo.py check
```

`demo-db.py` checks exact container image, loopback mapping, database comment, users/accounts and absence of provider credentials. It creates a custom-format dump in ignored `.local/demo/backups`, records SHA-256, checks the archive, restores it transactionally into a newly created verification database and compares table/row counts. Only that verified temporary database is dropped. Reset locks all public business tables, rechecks safety and truncates them together, retaining `goose_db_version`, schema, constraints and functions. No unknown Docker volume or other database is removed.

Reset creates a new synthetic owner through the ordinary registration flow and a new initial password. Do not keep using the previous password after reset. `prepare` creates only the isolated database if absent; it never clears an unmarked existing one.

## Launch and stop

Go builds use the repository-pinned toolchain; this workstation has no host Go executable, so builds and migrations were run with the existing Go Docker image. Build native Windows API and worker into ignored `.local/demo-api.exe` and `.local/demo-worker.exe` before running the helper. Apply the existing migrations only to the isolated database before `setup` grants runtime table permissions.

```powershell
python scripts/demo-db.py prepare
# Apply existing Goose migrations to the isolated local database using private environment configuration.
python scripts/dev-demo.py setup
python scripts/dev-demo.py start
# In web/: npm run dev
python scripts/dev-demo.py enroll
python scripts/dev-demo.py seed
```

`start` launches only API and worker, with hidden windows and logs in `.local/demo/`. Vite is separate. The helper removes inherited provider/test-publication configuration, selects merchant auth and uses a dedicated local DB role; no real provider key is supplied. `stop` verifies each saved executable path before terminating the owned process. Do not merge these Windows-specific helpers into production startup.

## Verification commands

Run `make lint`, `make test`, `go test -race ./...`, `make build`, `go build ./...`. Use a dedicated `_test` database for `make test-integration`. The seed integration test additionally requires a separate empty, marked `styk_demo_dev` instance through `TEST_DEMO_DATABASE_URL`; it must never target the working demo instance. Run `go test -race -tags=integration ./internal/devseed -count=1` there.

Frontend: typecheck, ESLint, Vitest, production build, and `python scripts/dev-demo.py smoke`. The last command passes the password only through a child-process environment, not command arguments or a checked-in file. Existing synchronization fail-closed gates stay enabled throughout.

For explicit referential verification in this marked development database only: `APP_ENV=development python scripts/demo-db.py verify --confirm styk_demo_dev`. This validates existing NOT VALID foreign keys; migration sources remain unchanged. Global mock inventory tables have no tenant field in the existing schema and are confined to the dedicated single-owner database.
