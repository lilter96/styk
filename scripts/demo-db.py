"""Prepare/backup/reset only the marked, isolated local STYK demo database."""
import argparse
import hashlib
import json
import os
import subprocess
from datetime import datetime, timezone
from pathlib import Path

CONTAINER = "styk-redesign-db"
DATABASE = "styk_demo_dev"
MARKER = "STYK isolated development demo v1"
ROOT = Path(__file__).resolve().parents[1]


def run(args, data=None):
    result = subprocess.run(args, input=data, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if result.returncode:
        raise RuntimeError("Local database command failed; no credentials printed")
    return result.stdout


def sql(query, database="postgres"):
    return run(["docker", "exec", "-i", CONTAINER, "psql", "-X", "-v", "ON_ERROR_STOP=1",
                "-U", "styk_ui", "-d", database, "-At"], query.encode()).decode().strip()


def inspect():
    data = json.loads(run(["docker", "inspect", CONTAINER]))[0]
    ports = data["HostConfig"]["PortBindings"].get("5432/tcp", [])
    if data["Config"]["Image"] != "postgres:17.6" or ports != [{"HostIp": "127.0.0.1", "HostPort": "55433"}]:
        raise RuntimeError("Expected local PostgreSQL image and loopback mapping not found")
    if not data["State"]["Running"]:
        raise RuntimeError("Local demo PostgreSQL container is not running")


def safe_database():
    marker = sql("SELECT coalesce(shobj_description(oid,'pg_database'),'') FROM pg_database WHERE datname=current_database()", DATABASE)
    if marker != MARKER:
        raise RuntimeError("Database lacks the exact isolated-development marker")
    if sql("SELECT to_regclass('public.saas_users') IS NOT NULL", DATABASE) == "t":
        unsafe = sql("SELECT EXISTS(SELECT 1 FROM saas_users WHERE email<>'demo@styk.local') OR EXISTS(SELECT 1 FROM connection_credentials) OR EXISTS(SELECT 1 FROM stock_connections WHERE account_id NOT LIKE 'STYK-DEMO-%')", DATABASE)
        if unsafe != "f":
            raise RuntimeError("Other users, external credentials or non-demo accounts exist; refusing mutation")


def backup():
    folder = ROOT / ".local" / "demo" / "backups"
    folder.mkdir(parents=True, exist_ok=True)
    stamp = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%S%fZ")
    dump = run(["docker", "exec", CONTAINER, "pg_dump", "-U", "styk_ui", "-d", DATABASE, "-Fc", "--no-owner"])
    listing = run(["docker", "exec", "-i", CONTAINER, "pg_restore", "--list"], dump)
    if not dump.startswith(b"PGDMP") or not listing:
        raise RuntimeError("Backup archive verification failed")
    path = folder / (stamp + ".dump")
    path.write_bytes(dump)
    path.with_suffix(".sha256").write_text(hashlib.sha256(dump).hexdigest() + "\n")
    return path, dump


def verify_restore(dump):
    # This name is generated here, created here, and checked again before removal.
    name = "styk_demo_backup_check_" + datetime.now(timezone.utc).strftime("%Y%m%d%H%M%S%f")
    sql('CREATE DATABASE "' + name + '"')
    sql("COMMENT ON DATABASE " + name + " IS 'STYK temporary backup verification'")
    run(["docker", "exec", "-i", CONTAINER, "pg_restore", "-U", "styk_ui", "-d", name,
         "--no-owner", "--exit-on-error", "--single-transaction"], dump)
    original = sql("SELECT count(*) FROM pg_tables WHERE schemaname='public'", DATABASE)
    restored = sql("SELECT count(*) FROM pg_tables WHERE schemaname='public'", name)
    names = sql("SELECT tablename FROM pg_tables WHERE schemaname='public' ORDER BY tablename", DATABASE).splitlines()
    if names:
        counts = " UNION ALL ".join("SELECT '" + name.replace("'", "''") + "',count(*) FROM public.\"" + name.replace('"', '""') + "\"" for name in names)
        if sorted(sql(counts, DATABASE).splitlines()) != sorted(sql(counts, name).splitlines()):
            raise RuntimeError("Restored row counts differ; verification database preserved")
    if restored != original:
        raise RuntimeError("Restored schema inventory mismatch; verification database preserved")
    marker = sql("SELECT shobj_description(oid,'pg_database') FROM pg_database WHERE datname=current_database()", name)
    if not name.startswith("styk_demo_backup_check_") or marker != "STYK temporary backup verification":
        raise RuntimeError("Refusing removal of an unverified database")
    sql('DROP DATABASE "' + name + '"')


def reset():
    tables = sql("SELECT string_agg(format('%I.%I',schemaname,tablename),',') FROM pg_tables WHERE schemaname='public' AND tablename<>'goose_db_version'", DATABASE)
    if not tables:
        return
    # Preserve schema and Goose history. All public business tables are included,
    # so no CASCADE can reach an unrelated schema.
    guard = "DO $$ BEGIN IF current_database()<>'styk_demo_dev' OR EXISTS(SELECT 1 FROM saas_users WHERE email<>'demo@styk.local') OR EXISTS(SELECT 1 FROM connection_credentials) OR EXISTS(SELECT 1 FROM stock_connections WHERE account_id NOT LIKE 'STYK-DEMO-%') THEN RAISE EXCEPTION 'unsafe reset target'; END IF; END $$;"
    sql("BEGIN; LOCK TABLE " + tables + " IN ACCESS EXCLUSIVE MODE; " + guard + " TRUNCATE " + tables + " RESTART IDENTITY; COMMIT;", DATABASE)


def verify():
    statements = sql("SELECT format('ALTER TABLE %I.%I VALIDATE CONSTRAINT %I;',n.nspname,c.relname,k.conname) FROM pg_constraint k JOIN pg_class c ON c.oid=k.conrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND k.contype='f' AND NOT k.convalidated ORDER BY c.relname,k.conname", DATABASE)
    if statements:
        sql("BEGIN; " + statements + " COMMIT;", DATABASE)
    remaining = sql("SELECT count(*) FROM pg_constraint k JOIN pg_class c ON c.oid=k.conrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND k.contype='f' AND NOT k.convalidated", DATABASE)
    if remaining != "0":
        raise RuntimeError("Foreign-key validation incomplete")
    print(json.dumps({"database": DATABASE, "foreign_keys_validated": True, "unvalidated": 0}))


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("action", choices=["prepare", "backup", "reset", "verify"])
    parser.add_argument("--confirm", default="")
    args = parser.parse_args()
    if args.action in {"reset", "verify"} and (args.confirm != DATABASE or os.environ.get("APP_ENV") != "development"):
        raise RuntimeError("Reset requires APP_ENV=development and --confirm styk_demo_dev")
    inspect()
    exists = sql("SELECT count(*) FROM pg_database WHERE datname='styk_demo_dev'") == "1"
    if not exists:
        if args.action != "prepare":
            raise RuntimeError("Demo database does not exist")
        sql("CREATE DATABASE styk_demo_dev")
        sql("COMMENT ON DATABASE styk_demo_dev IS '" + MARKER + "'")
    safe_database()
    if args.action == "verify":
        verify()
        return
    path, dump = backup()
    verify_restore(dump)
    if args.action == "reset":
        if args.confirm != DATABASE:
            raise RuntimeError("Reset requires --confirm styk_demo_dev")
        safe_database()
        reset()
    print(json.dumps({"database": DATABASE, "action": args.action,
                      "backup": str(path.relative_to(ROOT)), "restore_verified": True}))


if __name__ == "__main__":
    main()
