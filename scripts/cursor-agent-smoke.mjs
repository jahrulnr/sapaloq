#!/usr/bin/env node
/** Live api5 smoke — same handshake as 9router cursorAgent.js */
import http2 from "http2";
import { execSync } from "node:child_process";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import zlib from "node:zlib";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const repoRoot = path.resolve(__dirname, "..");
const nineRouter = process.env.SAPALOQ_9ROUTER_DIR || "/apps/other/9router";

const creds = JSON.parse(
  execSync(`go run ./internal/bridges/cursor/wire/cmd/credprint/`, {
    cwd: repoRoot,
    encoding: "utf8",
  }).trim()
);

const pb = await import(
  pathToFileURL(path.join(nineRouter, "open-sse/utils/cursorAgentProtobuf.js")).href
);
const chk = await import(
  pathToFileURL(path.join(nineRouter, "open-sse/utils/cursorChecksum.js")).href
);

const host = "agentn.global.api5.cursor.sh";
const reqPath = "/agent.v1.AgentService/Run";
const headers = {
  ...chk.buildCursorHeaders(creds.accessToken, creds.machineId, creds.ghostMode !== false),
  "x-cursor-client-type": "cli",
  "x-cursor-client-version": "cli-3.1.0",
};
const body = pb.buildAgentRequestBody({
  modelId: "default",
  userText: "Reply with exactly: pong",
  conversationId: "node-smoke",
});

const events = [];
const acked = new Set();
let errMsg = null;

await new Promise((resolve, reject) => {
  const client = http2.connect(`https://${host}`);
  const req = client.request({
    ":method": "POST",
    ":path": reqPath,
    ":authority": host,
    ":scheme": "https",
    ...headers,
  });
  let buf = Buffer.alloc(0);
  let status = 0;

  req.on("response", (h) => {
    status = Number(h[":status"] || 0);
  });

  req.on("data", (chunk) => {
    buf = Buffer.concat([buf, chunk]);
    let pos = 0;
    while (pos + 5 <= buf.length) {
      const flag = buf[pos];
      const len = buf.readUInt32BE(pos + 1);
      if (pos + 5 + len > buf.length) break;
      let payload = buf.subarray(pos + 5, pos + 5 + len);
      pos += 5 + len;
      if (flag & 0x1) payload = zlib.gunzipSync(payload);
      if (payload.length && payload[0] === 0x7b) {
        try {
          const j = JSON.parse(payload.toString("utf8"));
          const msg = j?.error?.message || j?.error?.code;
          if (msg) {
            errMsg = msg;
            req.close();
            client.close();
            resolve();
            return;
          }
        } catch {}
        continue;
      }
      const exec = pb.decodeExecServerEvent(payload);
      if (exec) {
        const key = `${exec.kind}:${exec.execId}:${exec.execMsgId}`;
        if (!acked.has(key)) {
          acked.add(key);
          if (exec.kind === "exec_request_context") {
            req.write(pb.encodeRequestContextResponse(exec.execMsgId, exec.execId, []));
          }
        }
      }
      for (const d of pb.decodeAgentServerMessage(payload)) {
        events.push(d);
      }
    }
    buf = buf.subarray(pos);
  });

  req.on("end", () => {
    client.close();
    if (!errMsg && status && status !== 200) errMsg = `http ${status}`;
    resolve();
  });
  req.on("error", (e) => reject(e));
  req.write(body);
});

const text = events.filter((e) => e.kind === "text").map((e) => e.text).join("");
console.log(JSON.stringify({ ok: !errMsg && text.length > 0, errMsg, events: events.map((e) => e.kind), text }, null, 2));
