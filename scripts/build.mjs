import { spawnSync } from "node:child_process";
import { runGo } from "./go-command.mjs";
const env = { ...process.env, VITE_NEON_REALTIME_URL: process.env.NEON_REALTIME_URL || "" };
const npm = process.env.npm_execpath;
const webBuild = ["--prefix", "web", "run", "build"];
const extension = process.platform === "win32" ? ".exe" : "";
for (const [command, args] of [
  npm ? [process.execPath, [npm, ...webBuild]] : ["npm", webBuild],
  ["go", ["build", "-o", "bin/styk-api" + extension, "./cmd/api"]],
  ["go", ["build", "-o", "bin/styk-worker" + extension, "./cmd/worker"]],
]) {
  const options = { env, stdio: "inherit" };
  const result = command === "go" ? runGo(args, options) : spawnSync(command, args, options);
  if (result.error) throw result.error;
  if (result.status !== 0) process.exit(result.status || 1);
}
