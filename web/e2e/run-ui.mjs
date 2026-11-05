// Isolated presentation tests. There is deliberately no application API proxy.
import { createServer } from "vite";
import react from "@vitejs/plugin-react";
import { spawn } from "node:child_process";
import { fileURLToPath, URL } from "node:url";
import { createServer as createHTTPServer } from "node:http";
import process from "node:process";
import console from "node:console";

const allowed = ["e2e/orders-console.spec.ts", "e2e/redesign.spec.ts"];
const specs = process.argv.slice(2);
if (specs.some((spec) => !allowed.includes(spec))) {
  console.error("test:ui accepts only the isolated presentation specs.");
  process.exitCode = 1;
} else {
  const root = fileURLToPath(new URL("../", import.meta.url));
  const httpServer = createHTTPServer();
  const server = await createServer({
    configFile: false,
    root,
    plugins: [react()],
    server: { middlewareMode: true, hmr: { server: httpServer } },
  });
  httpServer.on("request", server.middlewares);
  await new Promise((resolve, reject) => {
    httpServer.once("error", reject);
    httpServer.listen(0, "127.0.0.1", resolve);
  });
  const address = httpServer.address();
  if (!address || typeof address === "string")
    throw new Error("No loopback test server");
  const origin = `http://127.0.0.1:${address.port}`;
  const env = Object.fromEntries(
    Object.entries(process.env).filter(
      ([key]) =>
        !/^(MOYSKLAD_|OZON_|STYK_|PG|DATABASE_URL$|TEST_DATABASE_URL$)/.test(
          key,
        ),
    ),
  );
  Object.assign(env, {
    STYK_ORDERS_UI_ORIGIN: origin,
    STYK_UI_VISUAL_ORIGIN: origin,
  });
  const child = spawn(
    process.execPath,
    [
      "node_modules/@playwright/test/cli.js",
      "test",
      ...(specs.length ? specs : allowed),
      "--workers=1",
      "--reporter=line",
    ],
    {
      cwd: root,
      env,
      stdio: "inherit",
      detached: process.platform !== "win32",
    },
  );
  const stop = () => {
    if (!child.pid) return;
    try {
      if (process.platform === "win32") child.kill("SIGTERM");
      else process.kill(-child.pid, "SIGTERM");
    } catch (error) {
      if (error.code !== "ESRCH")
        console.error("Could not stop owned browser test process.");
    }
  };
  process.on("SIGINT", stop);
  process.on("SIGTERM", stop);
  try {
    process.exitCode = await new Promise((resolve, reject) => {
      child.once("error", reject);
      child.once("exit", (code) => resolve(code ?? 1));
    });
  } finally {
    process.off("SIGINT", stop);
    process.off("SIGTERM", stop);
    await server.close();
    httpServer.closeAllConnections();
    await new Promise((resolve, reject) =>
      httpServer.close((error) => (error ? reject(error) : resolve())),
    );
  }
}
