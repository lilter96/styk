# Статический security-аудит

## Scope и уровень уверенности

Никаких атак, login/probe requests, внешних API, DB reads или vulnerability package installation. Findings ниже основаны на source/config. Потенциальный риск не назван подтверждённой эксплуатацией. OWASP-подобные категории — способ организации, не сертификат.

## Защитные механизмы CONFIRMED

- bcrypt cost 12; password length 12–72 bytes; ограничение concurrent hashes четырьмя slots на identity.Store. identity/identity.go.
- Random opaque session token; в БД SHA-256 digest, отдельно random CSRF, audit ID; expiry до 8 часов; user auth_version/revocation/email_verified/disabled проверки. identity/sessions.go, migration 33.
- Cookie HttpOnly + SameSite Strict; Secure зависит от HTTPS origin; local HTTP exception ограничен loopback. console/merchant_public.go и merchant_auth.go.
- Exact Origin/Host и loopback peer, bounded trusted proxy CIDRs; CSRF constant-time compare на mutations.
- Public auth rate buckets в PostgreSQL, JSON unknown-field rejection; request/body size и duplicate tenant/body checks.
- AES-GCM provider credentials с tenant/provider/account/revision AAD, random nonce; DB-only attacker не может проверить guessed key через обычный digest благодаря HMAC. credentials/vault.go.
- Tenant из authenticated merchant binding; 404 для чужого scope/object; DB actor guard с locks/expiry recheck; immutable audit.
- Typed provider URL/method allowlists, bounded response/pagination, controlled write scopes и durable mutation intent.

## Реестр рисков

| ID | Severity | Тип/уверенность | Компонент и доказательство | Концептуальный сценарий / последствия | Рекомендация |
|---|---|---|---|---|---|
| S01 | HIGH | CONFIRMED config gap; leak INFERRED | .dockerignore; Dockerfile COPY . . | build context включает .local/ненужные артефакты, если они есть; удалённый builder/кэш получает sensitive material | exclude локальные артефакты, allowlisted context, проверить историю build caches |
| S02 | HIGH | CONFIRMED trust boundary; exploit UNKNOWN | merchant_scope.go; postgres/merchant_actor.go; migrations | новый незащищённый handler или SQL без scope обойдёт isolation; DB principal видит все tenant | route coverage, mandatory guard conventions, least-privilege DB; оценить RLS отдельно |
| S03 | HIGH | UNKNOWN operational control | credentials/vault.go FromEnvironment/Decrypt | потеря/утечка общего encryption key затронет credentials; простая замена ключа ломает decrypt | KMS/secret manager, versioned key IDs, rotation/restore rehearsal |
| S04 | HIGH | CONFIRMED restricted capability | stock_publication.go gate; testpublication; catalog worker gate | некорректное снятие gate допускает overselling/неверную запись для клиентов | отдельный production дизайн, acceptance scope, review, staged rollout |
| S05 | HIGH | UNKNOWN deployment control | configuredHandler; merchantAuth.safe | proxy неверно сохраняет Host/peer/Forwarded, делает auth недоступной или искажает rate-limit identity | formal reverse-proxy config, TLS, forwarded-header tests, no public direct bind |
| S06 | MEDIUM | CONFIRMED debt / latent risk | auth.go; session_fixture_integration.go | локальная fixture login не проверяет key; build tag integration + .test suffix — не security boundary для злоумышленника с build/deploy доступом | fixture только в test harness; запрет integration-tag artifacts в release; удалить legacy runtime fallback |
| S07 | MEDIUM | INFERRED availability | per-client rate limiter; credentials factories | множество клиентов/processes превышают общий vendor quota → 429, backlog, stale stock | account-scoped limiter/coordination и observability |
| S08 | MEDIUM | CONFIRMED missing product flow | merchant_routes.go; schema inventory | пользователь не может исполнить deletion/export/consent lifecycle; рост retention obligations | privacy lifecycle и signed-off policy |
| S09 | MEDIUM | UNKNOWN dependency exposure | go.mod, package-lock files | версии могут иметь известные advisories; аудит не делал vulnerability scan | lockfile SBOM/SCA в изолированном CI, review severity/reachability |
| S10 | MEDIUM | INFERRED starvation/DoS | application/worker.go Run | непрерывный stock/automatic backlog откладывает imports, freshness ухудшается | fair scheduling/quotas и queue age alerts |
| S11 | MEDIUM | UNKNOWN header/copy risks | SPA host не в repo; external fonts/maps | XSS defence/CSP/HSTS/third-party consent зависят от hosting | explicit CSP, HSTS, frame policy, external resource review |
| S12 | MEDIUM | CONFIRMED absent subsystem | routes/schema billing absence | добавление payments без signed webhook/idempotency создаст billing fraud | production billing design до приёма денег |
| S13 | LOW/MEDIUM | CONFIRMED logging surface; leakage UNKNOWN | generic worker slog error paths | SQL/provider context при будущих изменениях может раскрыть sensitive data | structured redaction tests и safe error taxonomy |
| S14 | MEDIUM | UNKNOWN recovery readiness | no backup/restore automation in infra | encrypted DB backup без key или key без DB не восстанавливает credentials | coordinated backup/restore и access separation |

Severity относительна для будущего коммерческого SaaS. Не означает немедленно доступную remote exploit в текущем loopback стенде.

## Authentication/authorization nuances

JWT, refresh-token grant, OAuth и MFA не найдены. Их отсутствие само по себе не уязвимость, opaque cookie sessions достаточны при корректной эксплуатации. Admin authorization нельзя отождествлять с public admin panel: schema имеет роли, текущий merchant binding требует OWNER; доверенные CLIs — отдельная административная поверхность.

Auth removal не удалил весь legacy infrastructure: auth.go сохраняет operatorSession/map/fallback cookie authorization и logout; production constructors больше не создают эти sessions. Генерация local fixture находится в тестовых файлах/build-tag file. Поэтому утверждение «совершенно весь старый auth-код удалён» было бы неточным. Нормальный runtime не регистрирует key login и local mode отвергает. Нужен отдельный cleanup review без ослабления защиты.

## SQL injection / XSS / IDOR / SSRF

Просмотренные SQL используют pgx parameters; динамические куски в store обычно строятся из фиксированных predicates. Подтверждённой SQL injection не установлено; exhaustive taint proof не проводился.

React по умолчанию экранирует текст; generic error client не показывает сырые response bodies. Полный XSS анализ всех content sinks и deployed CSP не выполнен. HTML legal/marketing — статические files.

IDOR mitigations: tenant injection, review session ownership, scoped account queries, composite constraints и actor guards. Нельзя экстраполировать одну защищённую ручку на все будущие endpoints; test matrix должна содержать чужие operation/review/account IDs.

Provider clients запрещают произвольные paths/hosts и redirect escalation в отдельных adapters. Полного image upload/fetch pipeline нет; не объявлять image URL field уже защищённой upload системой.

## Secrets handling в аудите

Значения секретов и .env не читались. В отчётах только названия env vars, имена методов/таблиц и нейтральные paths. Local login/DPAPI files не включены в ZIP. Source/CI может содержать fixed synthetic fixtures; они не перепечатаны как реальные credentials.

## Проверки следующего этапа

Dedicated тесты auth expiry за lock wait, IDOR по всем objects, revoked credentials in-flight, malformed JSON, egress allowlists/redirects, compressed response limits, per-target partial result и unknown outcome. Production или реальные provider attacks не нужны и не разрешены этим аудитом.


## Source evidence index

Static inspection only. Line numbers refer to this working tree, not a release tag. External sibling paths are intentionally absolute; source is not included in the ZIP.

| Source | Symbols / evidence lines |
|---|---|
| [.dockerignore:1](../../.dockerignore#L1) | configuration / declarations; see file |
| [Dockerfile:1](../../Dockerfile#L1) | configuration / declarations; see file |
| [internal/identity/identity.go:1](../../internal/identity/identity.go#L1) | 59: WithSession; 63: SessionFromContext; 68: New; 79: canonicalEmail; 88: validPassword; 92: validName; 96: identifier; 104: randomToken; 112: tokenHash; 121: passwordHash; 131: passwordMatches; 141: rollback |
| [internal/identity/sessions.go:1](../../internal/identity/sessions.go#L1) | 13: CreateSession; 37: insertSession; 60: Session; 80: RevokeSession |
| [internal/console/merchant_auth.go:1](../../internal/console/merchant_auth.go#L1) | 33: newMerchantAuth; 58: safe; 79: sameOrigin; 98: authorized; 133: readSession; 141: checkCSRF; 148: sessionFrom; 153: actorProvenance |
| [internal/console/merchant_scope.go:1](../../internal/console/merchant_scope.go#L1) | 18: authorizeScope; 47: scopeRequest; 72: isMappingReviewObject; 76: reviewWorkspace; 93: scopeMerchantBody; 125: merchantObject |
| [internal/console/merchant_public.go:1](../../internal/console/merchant_public.go#L1) | 17: publicRequest; 42: allowIdentity; 57: register; 83: login; 109: deliverVerification; 120: resendVerification; 131: verify |
| [internal/credentials/vault.go:1](../../internal/credentials/vault.go#L1) | 31: Valid; 34: validID; 50: Scope; 63: New; 80: FromEnvironment; 95: aad; 105: Encrypt; 122: Decrypt; 143: Digest; 161: Resolve; 168: ResolveWithRevision; 195: ScopeKey |
| [internal/postgres/merchant_actor.go:1](../../internal/postgres/merchant_actor.go#L1) | 15: auditPrincipal; 22: rollbackMerchantMutation; 30: requireContextActor; 38: ensureConnectionWorkspace; 49: requireMerchantActor |
| [internal/console/auth.go:1](../../internal/console/auth.go#L1) | 33: loopbackHost; 42: ValidateAddress; 51: safe; 67: sameOrigin; 74: authorized; 121: logout |
| [internal/console/session_fixture_integration.go:1](../../internal/console/session_fixture_integration.go#L1) | 20: newAuth; 30: login; 67: Handler; 73: handlerWithConnections; 84: fixtureHandler |
| [internal/application/worker.go:1](../../internal/application/worker.go#L1) | 38: Process; 62: Run; 150: advanceCatalog |
