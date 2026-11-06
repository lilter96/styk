import { spawnSync } from "node:child_process";
const database = process.env.DATABASE_URL_UNPOOLED || process.env.DATABASE_URL;
if (!database) throw new Error("Set DATABASE_URL or DATABASE_URL_UNPOOLED in the selected env file");
const action = process.argv[2] || "status";
if (!["up", "status"].includes(action)) throw new Error("Use up or status");
const result = spawnSync("go", ["run", "-tags=no_mysql no_sqlite3 no_mssql no_ydb no_vertica no_clickhouse no_libsql", "github.com/pressly/goose/v3/cmd/goose@v3.25.0", "-dir", "migrations", action], {
  env: { ...process.env, GOOSE_DRIVER: "postgres", GOOSE_DBSTRING: database }, stdio: "inherit",
});
if (result.error) throw result.error;
process.exit(result.status || 0);
