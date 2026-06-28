#!/usr/bin/env node
/**
 * Oprek helper: run `cursor agent --print --output-format stream-json` and
 * summarize the event contract for SapaLOQ bridge design.
 *
 * Usage:
 *   node scripts/cursor-agent-probe.mjs "hyy"
 *   node scripts/cursor-agent-probe.mjs --mode ask "buat web keren di /tmp/profile"
 *   echo "list /tmp" | node scripts/cursor-agent-probe.mjs -
 *
 * Stdout: one JSON object (not NDJSON) with normalized events + raw session_id.
 * Compare with: node scripts/cursor-node-stream.mjs  (api2 headless path)
 */
import { spawn } from "node:child_process";
import readline from "node:readline";

const args = process.argv.slice(2);
let mode = "";
const rest = [];
for (let i = 0; i < args.length; i++) {
  if (args[i] === "--mode" && args[i + 1]) {
    mode = args[++i];
  } else {
    rest.push(args[i]);
  }
}

const prompt = rest.join(" ").trim() || "reply with exactly: pong";
const cwd = process.env.SAPALOQ_CURSOR_AGENT_CWD || process.cwd();

const cmdArgs = [
  "agent",
  "--trust",
  "--print",
  "--output-format",
  "stream-json",
  "--stream-partial-output",
];
if (mode) cmdArgs.push("--mode", mode);
cmdArgs.push("-p", prompt);

const child = spawn("cursor", cmdArgs, { cwd, env: process.env });

const summary = {
  prompt,
  cwd,
  mode: mode || "default",
  session_id: null,
  thinking: [],
  assistant: "",
  tool_calls: [],
  result: null,
  errors: [],
  event_counts: {},
};

const rl = readline.createInterface({ input: child.stdout });
rl.on("line", (line) => {
  line = line.trim();
  if (!line.startsWith("{")) return;
  let ev;
  try {
    ev = JSON.parse(line);
  } catch {
    return;
  }
  summary.event_counts[ev.type] = (summary.event_counts[ev.type] || 0) + 1;
  if (ev.session_id) summary.session_id = ev.session_id;

  switch (ev.type) {
    case "thinking":
      if (ev.subtype === "delta" && ev.text) summary.thinking.push(ev.text);
      break;
    case "assistant": {
      const parts = ev.message?.content || [];
      for (const p of parts) {
        if (p.type === "text" && p.text) summary.assistant += p.text;
      }
      break;
    }
    case "tool_call": {
      if (ev.subtype !== "started") break;
      const tc = ev.tool_call || {};
      const shell = tc.shellToolCall?.args?.command;
      const name = shell ? "shell" : Object.keys(tc).find((k) => k.endsWith("ToolCall")) || "unknown";
      summary.tool_calls.push({
        call_id: ev.call_id,
        name,
        command: shell || null,
      });
      break;
    }
    case "result":
      summary.result = ev.result ?? null;
      break;
    default:
      break;
  }
});

child.stderr.on("data", (buf) => {
  const s = String(buf).trim();
  if (s) summary.errors.push(s);
});

child.on("close", (code) => {
  summary.exit_code = code;
  summary.thinking_text = summary.thinking.join("");
  delete summary.thinking;
  process.stdout.write(JSON.stringify(summary, null, 2) + "\n");
  process.exit(code === 0 ? 0 : 1);
});
