#!/usr/bin/env node
// Bounded loopback smoke/soak. Node standard library only; no host DNS changes.
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { createSocket } from "node:dgram";
import { createServer } from "node:http";
import { connect } from "node:net";
import { execFile, execFileSync, spawn } from "node:child_process";
import {
  appendFileSync,
  chmodSync,
  closeSync,
  mkdirSync,
  mkdtempSync,
  openSync,
  readFileSync,
  renameSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { arch, platform, release, tmpdir } from "node:os";
import path from "node:path";
import { setTimeout as sleep } from "node:timers/promises";
import { parseArgs, promisify } from "node:util";

const { values, positionals } = parseArgs({
  allowPositionals: true,
  options: {
    output: { type: "string" },
    duration: { type: "string", default: "0" },
    "reload-every": { type: "string", default: "3600" },
    "outage-every": { type: "string", default: "900" },
    "outage-for": { type: "string", default: "5" },
    "sample-every": { type: "string", default: "60" },
    qps: { type: "string", default: "5" },
    "skip-web": { type: "boolean", default: false },
  },
});
assert(
  positionals.length === 1 && values.output,
  "Usage: qualification.mjs BINARY --output NEW_DIRECTORY [--duration SECONDS]",
);
const args = Object.fromEntries(
  Object.entries(values).map(([key, value]) => [
    key.replaceAll("-", "_"),
    ["output", "skip-web"].includes(key) ? value : Number(value),
  ]),
);
assert(
  Number.isFinite(args.duration) &&
    args.duration >= 0 &&
    args.duration <= 604800,
  "duration must be 0..7 days",
);
assert(
  [
    args.reload_every,
    args.outage_every,
    args.outage_for,
    args.sample_every,
    args.qps,
  ].every((n) => Number.isFinite(n) && n > 0),
);
assert(
  args.outage_for < args.outage_every && args.qps <= 100,
  "invalid outage interval or QPS >100",
);
assert(
  process.geteuid?.() !== 0,
  "run the qualification harness as a nonroot user",
);
const binary = path.resolve(positionals[0]),
  output = path.resolve(args.output);
const binaryHash = createHash("sha256")
  .update(readFileSync(binary))
  .digest("hex");
mkdirSync(path.dirname(output), { recursive: true });
mkdirSync(output); // Do not overwrite earlier evidence.
const report = {
  schema: 1,
  started_utc: new Date().toISOString(),
  platform: `${platform()} ${release()} ${arch()}`,
  uid: process.geteuid?.(),
  binary_sha256: binaryHash,
  parameters: { binary, ...args },
  checks: [],
  qualified_72h: false,
};
const abort = new AbortController();
for (const name of ["SIGTERM", "SIGINT"])
  process.once(name, () => abort.abort(new Error(`Interrupted by ${name}`)));
const signal = abort.signal;
const delay = (ms) => sleep(ms, undefined, { signal });
const run = async (...argv) => {
  try {
    const result = await promisify(execFile)(binary, argv, {
      timeout: 10000,
      signal,
    });
    return { ...result, code: 0 };
  } catch (error) {
    if (typeof error.code !== "number") throw error;
    return { code: error.code, stderr: error.stderr, stdout: error.stdout };
  }
};
const ip = (value) => Buffer.from(value.split(".").map(Number));
function atomicWrite(file, text) {
  writeFileSync(`${file}.new`, text, { mode: 0o600 });
  renameSync(`${file}.new`, file);
}
function configText(
  root,
  fixtures,
  value = "192.0.2.1",
  dns = "127.0.0.1:0",
  admin = "127.0.0.1:0",
) {
  return `version: 1
dns:
  listen: [${JSON.stringify(dns)}]
  upstreams: [${JSON.stringify(fixtures.upstream)}]
  upstream_policy:
    timeout_ms: 300
    attempt_timeout_ms: 100
admin:
  listen: ${JSON.stringify(admin)}
paths:
  data_dir: ${JSON.stringify(path.join(root, "data"))}
  secrets_dir: ${JSON.stringify(path.join(root, "secrets"))}
records:
  - name: local.example
    type: A
    value: ${value}
    ttl: 30
lists:
  - id: fixture
    url: ${JSON.stringify(fixtures.listURL)}
    dialect: domains
    domain_kind: exact
    enabled: true
`;
}
async function startFixtures() {
  const udp = createSocket("udp4");
  const fixtures = { outage: false, listsOffline: false };
  const http = createServer((_request, response) => {
    response.writeHead(fixtures.listsOffline ? 503 : 200);
    response.end(fixtures.listsOffline ? "offline" : "list-blocked.example\n");
  });
  udp.on("message", (query, peer) => {
    if (query.length < 17 || fixtures.outage) return;
    let end = 12;
    while (end < query.length && query[end]) end += query[end] + 1;
    if (end + 5 > query.length) return;
    const header = Buffer.from([0, 0, 0x81, 0x80, 0, 1, 0, 1, 0, 0, 0, 0]);
    query.copy(header, 0, 0, 2);
    // TTL zero keeps upstream outages visible instead of serving cached data.
    const answer = Buffer.from([
      0xc0, 0x0c, 0, 1, 0, 1, 0, 0, 0, 0, 0, 4, 203, 0, 113, 7,
    ]);
    udp.send(
      Buffer.concat([header, query.subarray(12, end + 5), answer]),
      peer.port,
      peer.address,
    );
  });
  try {
    await new Promise((resolve, reject) => {
      udp.once("error", reject);
      udp.bind(0, "127.0.0.1", resolve);
    });
    await new Promise((resolve, reject) => {
      http.once("error", reject);
      http.listen(0, "127.0.0.1", resolve);
    });
  } catch (error) {
    udp.close();
    http.close();
    throw error;
  }
  return Object.assign(fixtures, {
    upstream: `127.0.0.1:${udp.address().port}`,
    listURL: `http://127.0.0.1:${http.address().port}/list`,
    close() {
      udp.close();
      http.closeAllConnections();
      http.close();
    },
  });
}
async function startDaemon(config, state) {
  const log = openSync(path.join(output, "daemon.log"), "a");
  const child = spawn(binary, ["serve", "-config", config, "-state", state], {
    stdio: ["ignore", "pipe", log],
  });
  closeSync(log);
  let spawnError;
  const exited = new Promise((resolve) => {
    child.once("error", (error) => {
      spawnError = error;
      resolve();
    });
    child.once("exit", resolve);
  });
  async function close() {
    if (child.exitCode === null && child.signalCode === null && !spawnError)
      child.kill("SIGTERM");
    let forced = false;
    const timer = setTimeout(() => {
      forced = true;
      child.kill("SIGKILL");
    }, 10000);
    try {
      await exited;
    } finally {
      clearTimeout(timer);
      child.stdout.destroy();
    }
    assert(!forced, "SIGTERM did not stop daemon within 10s");
    assert(!spawnError, String(spawnError));
    assert.equal(
      child.exitCode,
      0,
      `daemon exit ${child.exitCode}/${child.signalCode}`,
    );
  }
  try {
    const ready = await new Promise((resolve, reject) => {
      let text = "";
      const timer = setTimeout(
        () =>
          finish(new Error("startup did not emit readiness JSON within 20s")),
        20000,
      );
      const interrupted = () => finish(signal.reason);
      const stopped = () =>
        finish(spawnError || new Error("daemon exited before readiness"));
      const data = (chunk) => {
        text += chunk;
        if (!text.includes("\n")) return;
        try {
          finish(null, JSON.parse(text.slice(0, text.indexOf("\n"))));
        } catch (error) {
          finish(error);
        }
      };
      function finish(error, value) {
        clearTimeout(timer);
        signal.removeEventListener("abort", interrupted);
        child.removeListener("exit", stopped);
        child.removeListener("error", stopped);
        child.stdout.removeListener("data", data);
        if (error) reject(error);
        else resolve(value);
      }
      child.stdout.on("data", data);
      child.once("exit", stopped);
      child.once("error", stopped);
      signal.addEventListener("abort", interrupted, { once: true });
      if (signal.aborted) interrupted();
    });
    assert(ready.ready, JSON.stringify(ready));
    child.stdout.resume();
    return { ready, child, close };
  } catch (error) {
    await close().catch(() => {});
    throw error;
  }
}
async function query(address, name, tcp = false) {
  signal.throwIfAborted();
  const [host, port] = address.split(":");
  const message = Buffer.concat([
    Buffer.from([0x12, 0x34, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0]),
    ...name
      .split(".")
      .map((label) =>
        Buffer.concat([Buffer.from([label.length]), Buffer.from(label)]),
      ),
    Buffer.from([0, 0, 1, 0, 1]),
  ]);
  const response = await new Promise((resolve, reject) => {
    const socket = tcp
      ? connect({ host, port: Number(port) })
      : createSocket("udp4");
    let done = false,
      bytes = Buffer.alloc(0);
    const timer = setTimeout(
      () => finish(new Error("DNS query timed out")),
      2000,
    );
    const interrupted = () => finish(signal.reason);
    function finish(error, reply) {
      if (done) return;
      done = true;
      clearTimeout(timer);
      signal.removeEventListener("abort", interrupted);
      if (tcp) socket.destroy();
      else socket.close();
      if (error) reject(error);
      else resolve(reply);
    }
    signal.addEventListener("abort", interrupted, { once: true });
    socket.once("error", (error) => finish(error));
    if (tcp) {
      socket.once("connect", () => {
        const length = Buffer.alloc(2);
        length.writeUInt16BE(message.length);
        socket.write(Buffer.concat([length, message]));
      });
      socket.on("data", (chunk) => {
        bytes = Buffer.concat([bytes, chunk]);
        if (bytes.length >= 2 && bytes.length >= bytes.readUInt16BE(0) + 2)
          finish(null, bytes.subarray(2, bytes.readUInt16BE(0) + 2));
      });
      socket.once("end", () => finish(new Error("truncated TCP reply")));
    } else {
      socket.once("message", (reply) => finish(null, reply));
      socket.send(message, Number(port), host, (error) => {
        if (error) finish(error);
      });
    }
  });
  assert(
    response.length >= 12 &&
      response.readUInt16BE(0) === 0x1234 &&
      response[2] & 0x80,
    "bad DNS reply",
  );
  return response;
}
async function expectAddress(address, value, tcp = false) {
  const response = await query(address, "local.example", tcp);
  assert(
    (response[3] & 15) === 0 && response.subarray(-4).equals(ip(value)),
    response.toString("hex"),
  );
}
async function waitFor(check, timeout) {
  const deadline = performance.now() + timeout;
  while (true) {
    signal.throwIfAborted();
    try {
      await check();
      return;
    } catch (error) {
      if (performance.now() >= deadline) throw error;
    }
    await delay(100);
  }
}
const waitAddress = (address, value) =>
  waitFor(() => expectAddress(address, value), 5000);
let fixtures, daemon, root;
try {
  fixtures = await startFixtures();
  root = mkdtempSync(path.join(tmpdir(), "dimsum-qualification-"));
  const config = path.join(root, "dimsum.yaml"),
    state = path.join(root, "state");
  const source = configText(root, fixtures);
  atomicWrite(config, source);
  writeFileSync(path.join(output, "fixture.yaml"), source);
  let result = await run("validate", "-config", config);
  assert.equal(result.code, 0, result.stderr);
  report.checks.push("offline validation");
  chmodSync(config, 0);
  try {
    result = await run("validate", "-config", config);
    assert(
      result.code !== 0 && /permission/i.test(result.stderr),
      result.stderr,
    );
  } finally {
    chmodSync(config, 0o600);
  }
  report.checks.push("unreadable configuration rejected as nonroot");
  daemon = await startDaemon(config, state);
  let address = daemon.ready.dns[0];
  await expectAddress(address, "192.0.2.1");
  await expectAddress(address, "192.0.2.1", true);
  assert(
    (await query(address, "forward.example"))
      .subarray(-4)
      .equals(ip("203.0.113.7")),
  );
  assert(
    !(await query(address, "list-blocked.example"))
      .subarray(-4)
      .equals(ip("203.0.113.7")),
  );
  report.checks.push("nonroot UDP/TCP local, forwarding and list policy");
  if (!args.skip_web) {
    const url = `http://${daemon.ready.admin}`;
    const response = await fetch(url, {
      signal: AbortSignal.any([signal, AbortSignal.timeout(5000)]),
    });
    const body = await response.text();
    assert(
      response.status === 200 && /<html/i.test(body) && /<script/i.test(body),
      "embedded application assets missing",
    );
    const scripts = [...body.matchAll(/<script[^>]+src="([^"]+)"/g)].map(
      (match) => match[1],
    );
    assert(scripts.length, "HTML has no application script");
    for (const script of scripts) {
      assert(
        script.startsWith("/") && !script.startsWith("//"),
        "application script must be served locally",
      );
      const asset = await fetch(url + script, {
        signal: AbortSignal.any([signal, AbortSignal.timeout(5000)]),
      });
      assert(
        asset.status === 200 &&
          asset.headers.get("content-type")?.includes("javascript"),
        "embedded script missing or wrong MIME",
      );
      assert((await asset.arrayBuffer()).byteLength, "empty embedded script");
    }
    report.checks.push("embedded HTML and JavaScript application");
  }
  const conflict = path.join(root, "conflict.yaml");
  atomicWrite(conflict, configText(root, fixtures, "192.0.2.1", address));
  result = await run(
    "serve",
    "-config",
    conflict,
    "-state",
    path.join(root, "conflict-state"),
  );
  assert(result.code !== 0 && /bind/i.test(result.stderr), result.stderr);
  await expectAddress(address, "192.0.2.1");
  report.checks.push(
    "shared-host DNS port conflict leaves existing daemon serving",
  );
  atomicWrite(
    conflict,
    configText(root, fixtures, "192.0.2.1", "127.0.0.1:0", daemon.ready.admin),
  );
  result = await run(
    "serve",
    "-config",
    conflict,
    "-state",
    path.join(root, "admin-conflict-state"),
  );
  assert(result.code !== 0 && /bind/i.test(result.stderr), result.stderr);
  report.checks.push("admin port conflict");
  atomicWrite(config, configText(root, fixtures, "192.0.2.2"));
  await waitAddress(address, "192.0.2.2");
  atomicWrite(config, "version: invalid\n");
  await delay(300);
  await expectAddress(address, "192.0.2.2");
  report.checks.push(
    "atomic replacement reload; invalid edit retains active policy",
  );
  await daemon.close();
  daemon = undefined;
  fixtures.outage = true;
  fixtures.listsOffline = true;
  daemon = await startDaemon(config, state);
  address = daemon.ready.dns[0];
  await expectAddress(address, "192.0.2.2");
  assert(
    !(await query(address, "list-blocked.example"))
      .subarray(-4)
      .equals(ip("203.0.113.7")),
  );
  report.checks.push(
    "SIGTERM and offline invalid-config restart with retained list policy",
  );
  fixtures.outage = false;
  fixtures.listsOffline = false;
  atomicWrite(config, source);
  await waitAddress(address, "192.0.2.1");
  report.checks.push("corrected configuration activates after recovery");
  const start = performance.now(),
    samples = path.join(output, "samples.jsonl");
  writeFileSync(samples, "");
  let nextReload = args.reload_every,
    nextSample = 0,
    value = "192.0.2.1",
    count = 0,
    outages = 0,
    reloads = 0,
    wasOutage = false;
  while ((performance.now() - start) / 1000 < args.duration) {
    signal.throwIfAborted();
    const elapsed = (performance.now() - start) / 1000;
    assert(
      daemon.child.exitCode === null && daemon.child.signalCode === null,
      "daemon exited during soak",
    );
    const outage =
      elapsed >= args.outage_every &&
      elapsed % args.outage_every < args.outage_for;
    fixtures.outage = outage;
    if (outage && !wasOutage) outages++;
    wasOutage = outage;
    if (elapsed >= nextReload) {
      value = value === "192.0.2.1" ? "192.0.2.2" : "192.0.2.1";
      atomicWrite(config, configText(root, fixtures, value));
      await waitAddress(address, value);
      reloads++;
      nextReload += args.reload_every;
    }
    await expectAddress(address, value, count % 10 === 0);
    const response = await query(address, `q${count}.example`);
    assert([0, 2].includes(response[3] & 15), "unexpected upstream response");
    if (elapsed >= nextSample) {
      const rss = Number(
        execFileSync("ps", ["-o", "rss=", "-p", String(daemon.child.pid)], {
          encoding: "utf8",
        }).trim(),
      );
      assert(Number.isFinite(rss) && rss > 0, "invalid RSS measurement");
      appendFileSync(
        samples,
        JSON.stringify({
          elapsed_s: Number(elapsed.toFixed(3)),
          rss_kib: rss,
          queries: count,
          outage,
          reloads,
        }) + "\n",
      );
      nextSample += args.sample_every;
    }
    count++;
    await delay(1000 / args.qps);
  }
  report.soak = {
    elapsed_s: Number(((performance.now() - start) / 1000).toFixed(3)),
    iterations: count,
    reloads,
    outages,
    measurement:
      "sequential bounded load; RSS includes mapped/native memory; not retained Go heap",
  };
  fixtures.outage = false;
  await waitFor(async () => {
    const response = await query(address, "recovered.example");
    assert(
      (response[3] & 15) === 0 &&
        response.subarray(-4).equals(ip("203.0.113.7")),
      "forwarding did not recover",
    );
  }, 10000);
  report.checks.push("forwarding recovers after upstream outages");
  await daemon.close();
  daemon = undefined;
  report.checks.push("final SIGTERM exit zero");
  report.result = "passed";
} catch (error) {
  report.result = "failed";
  report.error = String(error.stack || error);
  process.exitCode = 1;
} finally {
  if (daemon) await daemon.close().catch(() => {});
  fixtures?.close();
  if (root) rmSync(root, { recursive: true, force: true });
  writeFileSync(
    path.join(output, "report.json"),
    JSON.stringify(report, null, 2) + "\n",
  );
  console.log(JSON.stringify(report, null, 2));
}
