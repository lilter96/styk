import { spawn } from "node:child_process";

// STYK uses session advisory locks, so transaction pooling is unsuitable.
const env = {
  ...process.env,
  DATABASE_URL: process.env.DATABASE_URL_UNPOOLED || process.env.DATABASE_URL,
  HTTP_ADDR: process.env.HTTP_ADDR || "127.0.0.1:8080",
  STYK_CONSOLE_ORIGIN: process.env.STYK_CONSOLE_ORIGIN || "http://127.0.0.1:5173",
  VITE_NEON_AUTH_URL: process.env.NEON_AUTH_BASE_URL,
  VITE_NEON_REALTIME_URL: process.env.NEON_REALTIME_URL,
};
if (!env.DATABASE_URL) throw new Error("Set DATABASE_URL or DATABASE_URL_UNPOOLED in the selected env file");

const grouped = process.platform !== "win32";
const options = { env, stdio: "inherit", detached: grouped };
const npm = process.env.npm_execpath;
const webDev = ["--prefix", "web", "run", "dev"];
const extension = grouped ? "" : ".exe";
const children = [
  spawn("./bin/styk-api" + extension, [], options),
  npm ? spawn(process.execPath, [npm, ...webDev], options) : spawn("npm", webDev, options),
];
let stopping = false;
function stop(code) {
  if (stopping) return;
  stopping = true;
  process.exitCode = code;
  for (const child of children) {
    if (!child.pid) continue;
    try {
      if (grouped) process.kill(-child.pid, "SIGTERM");
      else child.kill("SIGTERM");
    } catch (error) {
      if (error.code !== "ESRCH") {
        console.error("Could not stop a development process:", error.message);
        process.exitCode = 1;
      }
    }
  }
}
for (const signal of ["SIGINT", "SIGTERM"]) process.on(signal, () => stop(0));
for (const child of children) {
  child.on("error", (error) => {
    console.error(error.message);
    stop(1);
  });
  child.on("exit", (code) => stop(code ?? 1));
}
