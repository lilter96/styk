"""Windows local demo lifecycle. Secrets stay in current-user DPAPI files."""
import argparse
import base64
import ctypes
from ctypes import wintypes
from datetime import datetime, timezone
import http.cookiejar
import json
import os
from pathlib import Path
import re
import secrets
import subprocess
import urllib.error
import urllib.parse
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
LOCAL = ROOT / ".local" / "demo"
ORIGIN = "http://127.0.0.1:5173"
CONTAINER = "styk-redesign-db"


class Blob(ctypes.Structure):
    _fields_ = [("size", wintypes.DWORD), ("data", ctypes.POINTER(ctypes.c_char))]


def crypt(payload, protect):
    buffer = ctypes.create_string_buffer(payload)
    source = Blob(len(payload), ctypes.cast(buffer, ctypes.POINTER(ctypes.c_char)))
    target = Blob()
    function = ctypes.windll.crypt32.CryptProtectData if protect else ctypes.windll.crypt32.CryptUnprotectData
    if not function(ctypes.byref(source), None, None, None, None, 1, ctypes.byref(target)):
        raise RuntimeError("Current-user DPAPI operation failed")
    try:
        return ctypes.string_at(target.data, target.size)
    finally:
        ctypes.windll.kernel32.LocalFree(ctypes.cast(target.data, ctypes.c_void_p))


def save(config):
    LOCAL.mkdir(parents=True, exist_ok=True)
    (LOCAL / "runtime.dpapi").write_bytes(crypt(json.dumps(config).encode(), True))


def load():
    return json.loads(crypt((LOCAL / "runtime.dpapi").read_bytes(), False))


def sql(query):
    result = subprocess.run(["docker", "exec", "-i", CONTAINER, "psql", "-X", "-v", "ON_ERROR_STOP=1",
                             "-U", "styk_ui", "-d", "styk_demo_dev", "-At"], input=query.encode(),
                            stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if result.returncode:
        raise RuntimeError("Local demo database provisioning failed; credentials omitted")
    return result.stdout.decode().strip()


def setup():
    subprocess.run(["python", str(ROOT / "scripts/demo-db.py"), "prepare"], check=True)
    if (LOCAL / "runtime.dpapi").exists():
        return load()
    if sql("SELECT count(*) FROM pg_roles WHERE rolname='styk_demo'") != "0":
        raise RuntimeError("Existing styk_demo role without local DPAPI state; refusing to replace credentials")
    password = secrets.token_urlsafe(32)
    sql("CREATE ROLE styk_demo LOGIN PASSWORD '" + password + "'")
    sql("GRANT CONNECT ON DATABASE styk_demo_dev TO styk_demo; GRANT USAGE ON SCHEMA public TO styk_demo; GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA public TO styk_demo; GRANT USAGE,SELECT ON ALL SEQUENCES IN SCHEMA public TO styk_demo")
    config = {"database_url": "postgres://styk_demo:" + password + "@127.0.0.1:55433/styk_demo_dev?sslmode=disable",
              "encryption_key": base64.b64encode(secrets.token_bytes(32)).decode(),
              "as_of": datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")}
    save(config)
    return config


def environment(config):
    env = {key: value for key, value in os.environ.items()
           if not key.startswith(("STYK_", "MOYSKLAD_", "OZON_", "PG"))
           and key not in {"DATABASE_URL", "APP_ENV", "HTTP_ADDR"}}
    env.update(DATABASE_URL=config["database_url"], APP_ENV="development", HTTP_ADDR="127.0.0.1:8080",
               STYK_AUTH_MODE="merchant", STYK_CONSOLE_ORIGIN=ORIGIN,
               STYK_CREDENTIAL_ENCRYPTION_KEY=config["encryption_key"],
               STYK_SMTP_ADDRESS="localhost:1025", STYK_SMTP_FROM="styk@localhost",
               WORKER_POLL_INTERVAL="1s")
    return env


def process_path(pid):
    handle = ctypes.windll.kernel32.OpenProcess(0x1000, False, pid)
    if not handle:
        return None
    try:
        buffer = ctypes.create_unicode_buffer(32768)
        size = wintypes.DWORD(len(buffer))
        if not ctypes.windll.kernel32.QueryFullProcessImageNameW(handle, 0, buffer, ctypes.byref(size)):
            raise RuntimeError("Cannot verify saved process path")
        return Path(buffer.value).resolve()
    finally:
        ctypes.windll.kernel32.CloseHandle(handle)


def stop():
    for name in ["api", "worker"]:
        path = LOCAL / (name + ".pid")
        if not path.exists():
            continue
        pid = int(path.read_text())
        actual = process_path(pid)
        if actual is None:
            continue
        if actual != (ROOT / ".local" / ("demo-" + name + ".exe")).resolve():
            raise RuntimeError("Saved PID belongs to another executable; refusing to stop it")
        subprocess.run(["taskkill", "/PID", str(pid), "/F"], check=True, stdout=subprocess.DEVNULL)


def start(config):
    for name in ["api", "worker"]:
        pidfile = LOCAL / (name + ".pid")
        if pidfile.exists() and process_path(int(pidfile.read_text())):
            raise RuntimeError("Demo processes already running; stop them before restart")
        binary = ROOT / ".local" / ("demo-" + name + ".exe")
        with (LOCAL / (name + ".log")).open("ab") as log:
            process = subprocess.Popen([str(binary)], cwd=ROOT, env=environment(config), stdout=log,
                                       stderr=log, creationflags=subprocess.CREATE_NO_WINDOW)
        pidfile.write_text(str(process.pid))
    print("Local merchant API and safe worker started; publication gates remain disabled")


def client():
    return urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))


def post(opener, path, body):
    request = urllib.request.Request(ORIGIN + path, data=json.dumps(body).encode(),
                                    headers={"Origin": ORIGIN, "Content-Type": "application/json"})
    with opener.open(request, timeout=30) as response:
        return response.status


def enroll(config):
    if "owner_password" in config and sql("SELECT EXISTS(SELECT 1 FROM saas_users WHERE email='demo@styk.local')") == "t":
        check(config)
        return
    opener = client()
    post(opener, "/api/registration", {"email": "demo@styk.local"})
    with urllib.request.urlopen("http://127.0.0.1:8025/api/v1/messages", timeout=10) as response:
        messages = json.load(response)["messages"]
    message = next(item for item in messages if any(recipient["Address"] == "demo@styk.local" for recipient in item["To"]))
    with urllib.request.urlopen("http://127.0.0.1:8025/api/v1/message/" + message["ID"], timeout=10) as response:
        content = json.load(response)
    match = re.search(r"#verify=([A-Za-z0-9_-]+)", content["Text"])
    if not match:
        raise RuntimeError("Local verification link not found")
    password = secrets.token_urlsafe(32)
    post(opener, "/api/verification", {"token": match[1], "password": password})
    config["owner_password"] = password
    save(config)
    (LOCAL / "initial-password.dpapi").write_bytes(crypt(password.encode(), True))
    check(config)


def check(config):
    opener = client()
    status = post(opener, "/api/session", {"email": "demo@styk.local", "password": config["owner_password"]})
    with opener.open(ORIGIN + "/api/session", timeout=15) as response:
        session = json.load(response)
    print(json.dumps({"login": status, "session": 200, "tenant": session["account_id"],
                      "csrf_present": bool(session["csrf"])}))


def seed(config):
    env = environment(config)
    env["DATABASE_URL"] = config["database_url"].replace(":55433/", ":5432/")
    command = ["docker", "run", "--rm", "--network", "container:" + CONTAINER,
               "-v", str(ROOT) + ":/workspace", "-v", "styk-redesign-go:/go",
               "-v", "styk-redesign-build:/root/.cache/go-build", "-w", "/workspace",
               "-e", "DATABASE_URL", "-e", "APP_ENV", "golang:1.27.3",
               "go", "run", "./cmd/demo-seed", "-seed", "42", "-as-of", config["as_of"]]
    subprocess.run(command, env=env, check=True)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("action", choices=["setup", "start", "stop", "enroll", "seed", "check", "password", "smoke"])
    args = parser.parse_args()
    if os.name != "nt":
        raise RuntimeError("This lifecycle helper uses Windows current-user DPAPI")
    if args.action == "stop":
        stop()
        return
    config = setup() if args.action == "setup" else load()
    if args.action in {"start", "enroll", "seed", "check"}:
        globals()[args.action](config)
    if args.action == "smoke":
        env = environment(config)
        env["STYK_DEMO_TEST_PASSWORD"] = config["owner_password"]
        subprocess.run(["npx.cmd", "playwright", "test", "e2e/demo-dev.spec.ts", "--workers=1", "--reporter=line"], cwd=ROOT / "web", env=env, check=True)
    if args.action == "password":
        path = LOCAL / "initial-password.dpapi"
        print(crypt(path.read_bytes(), False).decode())
        path.unlink()


if __name__ == "__main__":
    main()
