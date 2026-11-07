import { spawn } from "node:child_process";
import process from "node:process";
import { fileURLToPath, URL } from "node:url";
import console from "node:console";
import { setTimeout as delay } from "node:timers/promises";
import { setTimeout, clearTimeout } from "node:timers";

if (process.platform === "win32") {
  console.error("The browser process stack requires POSIX process groups.");
  process.exitCode = 1;
} else {
  // The Go runner validates an optional dedicated loopback DSN, otherwise owns
  // a disposable database, and scrubs credentials before running the stack.
  const env = Object.fromEntries(
    Object.entries(process.env).filter(
      ([key]) =>
        !/^(MOYSKLAD_|OZON_|STYK_|PG|NEON_|AWS_|VITE_NEON_|DATABASE_URL)/.test(key),
    ),
  );
  const child = spawn(
    "go",
    ["run", "./cmd/checks", "run", "--lane", "browser-stack"],
    {
      cwd: fileURLToPath(new URL("../../", import.meta.url)),
      env,
      stdio: "inherit",
      detached: true,
    },
  );
  let stopping;
  const signalGroup = (signal) => {
    if (!child.pid) return;
    try {
      process.kill(-child.pid, signal);
    } catch (error) {
      if (error.code !== "ESRCH")
        console.error("Unable to stop the owned browser test process group.");
    }
  };
  const stop = () => {
    if (stopping) return;
    signalGroup("SIGTERM");
    // Allow the runner to remove its owned database inside the remaining twenty
    // seconds, then stop even a TERM-ignoring wrapper or descendant.
    stopping = delay(20_000).then(() => signalGroup("SIGKILL"));
  };
  const deadline = setTimeout(stop, 280_000);
  process.on("SIGINT", stop);
  process.on("SIGTERM", stop);
  try {
    process.exitCode = await new Promise((resolve) => {
      child.once("error", () => {
        console.error("Unable to start Go browser integration harness.");
        resolve(1);
      });
      child.once("exit", (code) => resolve(stopping ? 1 : (code ?? 1)));
    });
  } finally {
    clearTimeout(deadline);
    if (stopping) await stopping;
    process.off("SIGINT", stop);
    process.off("SIGTERM", stop);
  }
}
