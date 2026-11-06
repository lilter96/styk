import { historyQuery, realtimeServer } from "./history-query.mjs";

try {
  let input = "";
  for await (const chunk of process.stdin) {
    input += chunk;
    if (input.length > 4096) throw new Error("Input too large");
  }
  const sealed = await realtimeServer().seal({ query: historyQuery(JSON.parse(input)) });
  process.stdout.write(JSON.stringify(sealed));
} catch {
  // Never print query parameters, credentials or bearer capabilities.
  process.exitCode = 1;
}
