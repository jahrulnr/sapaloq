#!/usr/bin/env node
/**
 * Cursor chat helper for sapaloq when Go HTTP/2 wire is rejected by api2.
 * Stdin JSON: {
 *   "messages": [{ "role", "content" }],
 *   "model": "default",
 *   "accessToken": "...",
 *   "machineId": "...",
 *   "ghostMode": true,
 *   "tools": [],
 *   "forceAgentMode": true,
 *   "instruction": "OpenAI bridge: ...",
 *   "reasoningEffort": "medium"
 * }
 * Credentials are supplied by sapaloq-core (Go vscdb loader) so this script
 * does not need better-sqlite3 under systemd's Node version.
 * Stdout JSON: { "ok", "thinking", "content", "toolCalls", "error" }
 */
import path from "node:path";

const bridgeRoot = process.env.SAPALOQ_CURSOR_BRIDGE_DIR || "/apps/other/cursor-bridge";

async function readStdin() {
  const chunks = [];
  for await (const chunk of process.stdin) chunks.push(chunk);
  return JSON.parse(Buffer.concat(chunks).toString("utf8") || "{}");
}

async function loadCredentials(input) {
  if (input.accessToken && input.machineId) {
    return {
      accessToken: input.accessToken,
      machineId: input.machineId,
      ghostMode: input.ghostMode !== false,
    };
  }
  const loaderPath = path.join(bridgeRoot, "packages/credential-loader/src/index.js");
  const { loadCursorCredentials } = await import(loaderPath);
  return loadCursorCredentials();
}

async function main() {
  const probePath = path.join(bridgeRoot, "packages/cursor-proto-lab/src/probe.js");
  const { probeCursorChat } = await import(probePath);

  const input = await readStdin();
  const creds = await loadCredentials(input);
  const result = await probeCursorChat({
    accessToken: creds.accessToken,
    machineId: creds.machineId,
    ghostMode: creds.ghostMode,
    model: input.model || "default",
    messages: input.messages || [],
    tools: input.tools || [],
    forceAgentMode: !!input.forceAgentMode,
    instruction: input.instruction || "",
    reasoningEffort: input.reasoningEffort || null,
  });
  const payload = {
    ok: !!result.ok,
    status: result.status || 0,
    thinking: result.thinking || "",
    content: result.content || "",
    toolCalls: result.toolCalls || [],
    error: result.error || null,
  };
  process.stdout.write(JSON.stringify(payload));
  process.exit(result.ok ? 0 : 2);
}

main().catch((err) => {
  process.stdout.write(JSON.stringify({ ok: false, error: String(err.message || err) }));
  process.exit(1);
});
