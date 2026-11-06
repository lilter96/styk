import { spawnSync } from "node:child_process";
import { join } from "node:path";

export function runGo(args, options) {
  const result = spawnSync("go", args, options);
  if (
    result.error?.code !== "ENOENT" ||
    process.platform !== "win32" ||
    !process.env.LOCALAPPDATA
  ) {
    return result;
  }
  return spawnSync(join(process.env.LOCALAPPDATA, "Programs", "go", "bin", "go.exe"), args, options);
}
