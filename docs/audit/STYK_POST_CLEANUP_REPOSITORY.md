# Current repository map

```text
cmd/api/                   merchant HTTP API and health
cmd/worker/                durable jobs and safe/gated runners
cmd/demo-seed/             new explicit development fixture CLI
cmd/...                    preserved administrative/import/diagnostic CLIs
internal/console/          business HTTP and merchant auth; no runtime operator map
internal/identity/         unchanged users, verification/reset, bcrypt, sessions
internal/credentials/      unchanged encrypted provider-key handling
internal/postgres/         unchanged production SQL/workflow implementation
internal/application/      preserved orchestration and worker
internal/domain/           preserved scoped business models
internal/catalog*/         existing catalog facts/plan/provider/workflow
internal/commitment/       existing conservative stock calculation
internal/stocksync/        existing fail-closed stock runners
internal/infrastructure/   MoySklad and Ozon adapters
internal/sessionfixture/   consolidated synthetic Go-test sessions
internal/devseed/          development guards, transaction, counts and embedded SQL
migrations/                existing Goose migrations 1–44, unchanged
scripts/demo-db.py          marked local DB prepare/backup/reset/verify
scripts/dev-demo.py         Windows DPAPI lifecycle/enroll/seed/check/smoke/password
web/src/                   preserved real React console and design
web/e2e/demo-dev.spec.ts    new actual-local-app smoke tests
tests/integration/         preserved DB/security/concurrency suite; replay helper fixed
docs/development/          reproducible development workflow
docs/audit/                previous audit + current post-cleanup reports
Dockerfile/compose.yaml    preserved runtime/DB architecture
Makefile                   lint excludes generated/local trees
.gitignore/.dockerignore   generated/private artifact hygiene
.gitattributes             Go/shell LF checkout discipline
```

Sibling styk-demo and styk-landing remain separate sources. They were built and previewed, not refactored or merged into the real console. Their presentation-only WB/Yandex/order/analytics features are not operational backend capabilities.

Go dependency versions did not change. x/crypto was correctly classified as a direct dependency after go mod tidy. No frontend dependency was removed: React, ReactDOM, Router and TanStack Query are used; demo animation/3D/chart libraries are used by dynamic presentation paths.
