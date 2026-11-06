import { spawnSync } from "node:child_process";
const env = { ...process.env, VITE_NEON_REALTIME_URL: process.env.NEON_REALTIME_URL || "" };
for (const [command, args] of [
  ["npm", ["--prefix", "web", "run", "build"]],
  ["go", ["build", "-o", "bin/styk-api", "./cmd/api"]],
  ["go", ["build", "-o", "bin/styk-worker", "./cmd/worker"]],
]) {
  const result = spawnSync(command, args, { env, stdio: "inherit" });
  if (result.error) throw result.error;
  if (result.status !== 0) process.exit(result.status || 1);
}
