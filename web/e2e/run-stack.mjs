import { spawn } from "node:child_process";
import process from "node:process";
import { fileURLToPath, URL } from "node:url";
import console from "node:console";

function testEnvironment() {
  const dsn = process.env.TEST_DATABASE_URL;
  if (!dsn)
    throw new Error(
      "TEST_DATABASE_URL is required: use the dedicated localhost styk_orders_release_test or styk_orders_store_test database.",
    );
  let url;
  try {
    url = new URL(dsn);
  } catch {
    throw new Error(
      "TEST_DATABASE_URL must be a dedicated localhost PostgreSQL URL.",
    );
  }
  if (
    !["postgres:", "postgresql:"].includes(url.protocol) ||
    !["localhost", "127.0.0.1"].includes(url.hostname) ||
    !["/styk_orders_release_test", "/styk_orders_store_test"].includes(
      url.pathname,
    )
  ) {
    throw new Error(
      "Browser E2E refuses a non-test database; no services were started.",
    );
  }
  const env = { ...process.env };
  for (const name of Object.keys(env)) {
    if (
      /^(MOYSKLAD_|OZON_|STYK_TEST_|STYK_STOCK_PUBLICATION|STYK_CREDENTIAL_ENCRYPTION_KEY|STYK_OPERATOR_SECRET|DATABASE_URL$)/.test(
        name,
      )
    )
      delete env[name];
  }
  env.STYK_ORDERS_BROWSER_E2E = "1";
  return env;
}

try {
  if (process.platform === "win32")
    throw new Error("The browser process stack requires POSIX process groups.");
  const env = testEnvironment();
  console.log(
    "Running isolated merchant/order E2E: PostgreSQL, 3 APIs, 2 schedulers, 4 workers and loopback providers. Live credentials are excluded.",
  );
  const child = spawn(
    "go",
    [
      "test",
      "-race",
      "-tags=integration",
      "./tests/integration",
      "-run",
      "^TestOrdersNineProcessStack$",
      "-count=1",
      "-timeout=8m",
      "-v",
    ],
    {
      cwd: fileURLToPath(new URL("../../", import.meta.url)),
      env,
      stdio: "inherit",
      detached: true,
    },
  );
  let interrupted = false;
  const stopGroup = (signal) => {
    if (!child.pid) return;
    try {
      process.kill(-child.pid, signal);
    } catch (error) {
      if (error.code !== "ESRCH") {
        console.error("Unable to stop the owned browser test process group.");
        process.exitCode = 1;
      }
    }
  };
  const interrupt = () => {
    interrupted = true;
    stopGroup("SIGINT");
  };
  const terminate = () => {
    interrupted = true;
    stopGroup("SIGTERM");
  };
  process.on("SIGINT", interrupt);
  process.on("SIGTERM", terminate);
  child.once("error", () => {
    console.error("Unable to start Go browser integration harness.");
    process.exitCode = 1;
  });
  child.once("exit", (code) => {
    if (interrupted) stopGroup("SIGKILL");
    process.off("SIGINT", interrupt);
    process.off("SIGTERM", terminate);
    process.exitCode = interrupted ? 1 : (code ?? 1);
  });
} catch (error) {
  console.error(error.message);
  process.exitCode = 1;
}
