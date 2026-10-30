# Infrastructure report

| Service | Technology | Address / port | Status | Verification |
|---|---|---|---|---|
| Database | PostgreSQL 17.6, styk-redesign-db | 127.0.0.1:55433, styk_demo_dev | Running | pg_isready; API readiness; FK validation |
| Mail catcher | existing Mailpit, styk-redesign-mail | HTTP 8025; STARTTLS SMTP 1025 | Healthy | Docker healthy; actual local verification delivered |
| API | native Go executable | http://127.0.0.1:8080 | Running | /health/live and /health/ready 200; session/read API smoke |
| Worker | one native Go process | no HTTP readiness endpoint | Running | verified PID/executable; 12 mock tasks succeeded |
| Real console | React/Vite | http://127.0.0.1:5173 | Running | six pages; two Playwright tests; no JS console/page errors |
| Standalone demo | sibling styk-demo Vite | http://127.0.0.1:5185 | Running | build + HTTP 200 |
| Landing preview | sibling styk-landing Vite preview | http://127.0.0.1:5186 | Running | build; root/nested demo/three legal pages 200 |

The temporary `styk-demo-seed-check` PostgreSQL instance was used only for isolated seed integration tests with tmpfs data, then stopped/removed. No unknown Docker volume was cleared.

API/worker were cross-built with repository-pinned Go 1.27.3 Docker tooling and restarted on the final binaries. Existing database and Mailpit containers were reused; no new Redis/broker/.NET dependency was added.

## Commands

From the repository root:

```powershell
python scripts/dev-demo.py start
python scripts/dev-demo.py check
python scripts/dev-demo.py smoke
python scripts/dev-demo.py stop
```

Vite is independent: `npm run dev` in web. Presentation servers were started with existing Vite executables and explicit loopback ports; stop their recorded PIDs only after verifying command/path. Their PID/log files are in ignored `.local/demo/`.

Native API/worker paths: `.local/demo-api.exe`, `.local/demo-worker.exe`. Their runtime DPAPI/PID/log state is `.local/demo/`. No user secret is passed as a command argument or stored plaintext in committed config.

The launcher removes inherited STYK/provider/PG overrides and supplies only the local DB role, local encryption key, merchant origin and local SMTP settings. Publication/catalog test gates remain default-off. Stored provider credentials are absent; queued import examples cannot dispatch to real providers.

These are development processes, not production services with supervision/HA. PostgreSQL container has no assumed custom Docker healthcheck: pg_isready is the explicit readiness proof. Mailpit has an actual Docker healthy status.
