#!/usr/bin/env node
/**
 * Thin HTTP/2 transport gateway for SapaLOQ api5 agent path.
 * No protobuf, credentials logic, or exec handling — Go owns all of that.
 *
 * Protocol (newline-delimited JSON on stdin/stdout):
 *
 * stdin line 1 (config):
 *   { host, path, headers, bodyB64, timeoutMs? }
 * stdin lines 2+ (uplink, optional):
 *   { t: "write", b64: "..." }  — DATA on request stream
 *   { t: "close" }              — half-close upload
 *
 * stdout:
 *   { t: "status", code: 200 }
 *   { t: "data", b64: "..." }   — response DATA chunk (opaque bytes)
 *   { t: "end" }
 *   { t: "err", msg: "..." }
 */
import http2 from "node:http2";
import readline from "node:readline";

function emit(obj) {
  process.stdout.write(`${JSON.stringify(obj)}\n`);
}

function readConfig() {
  return new Promise((resolve, reject) => {
    const rl = readline.createInterface({ input: process.stdin, crlfDelay: Infinity });
    let gotLine = false;
    rl.once("line", (line) => {
      gotLine = true;
      try {
        resolve({ config: JSON.parse(line), rl });
      } catch (err) {
        rl.close();
        reject(err);
      }
    });
    rl.once("close", () => {
      if (!gotLine) reject(new Error("gateway: empty stdin"));
    });
  });
}

const { config, rl } = await readConfig();
const host = String(config.host || "").trim();
const path = String(config.path || "/").trim();
const headers = config.headers && typeof config.headers === "object" ? config.headers : {};
const body = config.bodyB64 ? Buffer.from(config.bodyB64, "base64") : Buffer.alloc(0);
const timeoutMs =
  Number.isInteger(config.timeoutMs) && config.timeoutMs > 0 ? config.timeoutMs : 120_000;

if (!host) {
  emit({ t: "err", msg: "gateway: host required" });
  process.exit(1);
}

let settled = false;
const shutdown = (err) => {
  if (settled) return;
  settled = true;
  clearTimeout(timer);
  try {
    rl.close();
  } catch {}
  if (err) {
    emit({ t: "err", msg: err instanceof Error ? err.message : String(err) });
    process.exit(2);
  }
  emit({ t: "end" });
  process.exit(0);
};

const timer = setTimeout(() => shutdown(new Error("gateway: stream timed out")), timeoutMs);

await new Promise((resolve) => {
  const client = http2.connect(`https://${host}`);
  const h2Headers = {
    ":method": "POST",
    ":path": path,
    ":authority": host,
    ":scheme": "https",
    ...headers,
  };
  const req = client.request(h2Headers);

  req.on("response", (h) => {
    emit({ t: "status", code: Number(h[":status"] || 0) });
  });

  req.on("data", (chunk) => {
    emit({ t: "data", b64: Buffer.from(chunk).toString("base64") });
  });

  req.on("end", () => {
    try {
      client.close();
    } catch {}
    resolve();
  });

  req.on("error", (err) => {
    try {
      client.close();
    } catch {}
    shutdown(err);
    resolve();
  });

  client.on("error", (err) => {
    shutdown(err);
    resolve();
  });

  rl.on("line", (line) => {
    if (settled) return;
    let msg;
    try {
      msg = JSON.parse(line);
    } catch {
      return;
    }
    if (msg?.t === "write" && msg.b64) {
      try {
        req.write(Buffer.from(msg.b64, "base64"));
      } catch (err) {
        shutdown(err);
        resolve();
      }
    } else if (msg?.t === "close") {
      try {
        req.close();
      } catch {}
    }
  });

  try {
    if (body.length > 0) req.write(body);
  } catch (err) {
    shutdown(err);
    resolve();
  }
});

shutdown(null);
