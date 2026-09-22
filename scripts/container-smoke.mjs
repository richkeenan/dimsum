#!/usr/bin/env node
// Exercise the Desktop Compose deployment with isolated ports and disposable volumes.
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import { Resolver } from "node:dns/promises";
import { createSocket } from "node:dgram";
import { connect, createServer } from "node:net";
import { fileURLToPath } from "node:url";

assert(process.argv[2], "Usage: node scripts/container-smoke.mjs IMAGE");
// Reserve on the host first: Desktop's forwarding does not consistently handle
// a published port of zero, even when the engine reports an assigned VM port.
async function freePort() {
  const tcp = createServer();
  const udp = createSocket("udp4");
  try {
    await new Promise((resolve, reject) => { tcp.once("error", reject); tcp.listen(0, "127.0.0.1", resolve); });
    const port = tcp.address().port;
    await new Promise((resolve, reject) => { udp.once("error", reject); udp.bind(port, "127.0.0.1", resolve); });
    return String(port);
  } finally {
    await new Promise((resolve) => tcp.close(resolve));
    try { udp.close(); } catch { /* Binding may have failed. */ }
  }
}
const project = `dimsum-test-${randomUUID().slice(0, 8)}`;
const started = new Date().toISOString();
const env = {
  ...process.env,
  DIMSUM_IMAGE: process.argv[2],
  // Match the shipped LAN DNS binding. Some Desktop versions don't forward
  // loopback-only published DNS ports even though TCP health checks connect.
  DIMSUM_DNS_BIND: "0.0.0.0",
  DIMSUM_ADMIN_BIND: "127.0.0.1",
  DIMSUM_DNS_PORT: await freePort(),
  DIMSUM_ADMIN_PORT: await freePort(),
};
const args = ["compose", "-p", project, "-f", fileURLToPath(new URL("../deploy/compose.desktop.yaml", import.meta.url))];
function docker(...args) {
  return execFileSync("docker", args, { env, encoding: "utf8", timeout: 180000 });
}
const compose = (...rest) => docker(...args, ...rest);
const control = (...rest) => JSON.parse(compose("exec", "-T", "dimsum", "dimsum", "control", "--socket", "/run/dimsum/control.sock", ...rest));
const port = (containerPort, protocol = "tcp") => Number(compose("port", "--protocol", protocol, "dimsum", String(containerPort)).trim().split(":").at(-1));
function allowPublishedAdminPort() {
  control("patch", "settings", JSON.stringify({
    revision: control("settings").status.saved_revision,
    edits: [{ path: ["admin", "allowed_hosts"], value: [`127.0.0.1:${port(8080)}`] }],
  }));
}

async function checkDNS() {
  const resolver = new Resolver({ timeout: 2000, tries: 1 });
  resolver.setServers([`127.0.0.1:${port(53, "udp")}`]);
  assert.deepEqual(await resolver.resolve4("desktop.example"), ["192.0.2.42"]);
  // The same synthetic record must resolve through the published TCP port.
  const query = Buffer.from("123401000001000000000000076465736b746f70076578616d706c650000010001", "hex");
  const length = Buffer.alloc(2);
  length.writeUInt16BE(query.length);
  await new Promise((resolve, reject) => {
    const socket = connect(port(53), "127.0.0.1");
    let data = Buffer.alloc(0);
    const fail = (err) => { socket.destroy(); reject(err); };
    socket.setTimeout(3000, () => fail(new Error("TCP DNS timed out")));
    socket.on("error", fail);
    socket.on("connect", () => socket.write(Buffer.concat([length, query])));
    socket.on("data", (chunk) => {
      data = Buffer.concat([data, chunk]);
      if (data.length < 2 || data.length < data.readUInt16BE(0) + 2) return;
      try {
        assert.equal(data.readUInt16BE(2), 0x1234);
        assert.equal(data[5] & 15, 0);
        assert.equal(data.readUInt16BE(8), 1);
        assert.deepEqual([...data.subarray(-4)], [192, 0, 2, 42]);
        socket.destroy(); resolve();
      } catch (err) { fail(err); }
    });
    socket.on("end", () => fail(new Error("TCP DNS closed before an answer")));
  });
}

try {
  compose("up", "-d", "--wait", "--wait-timeout", "90", "--pull", "never");
  const status = control("dhcp-status");
  assert.equal(status.availability.supported, false);
  assert.equal(status.availability.code, "docker_desktop");
  assert.match(status.availability.reason, /Docker Desktop/);
  assert.equal(status.dhcp.applied_enabled, false);

  // This harness uses random host ports. Admit that exact origin, as an operator
  // would when choosing a nondefault dashboard port or a reverse proxy.
  allowPublishedAdminPort();

  const base = `http://127.0.0.1:${port(8080)}`;
  const page = await fetch(base, { headers: { Connection: "close" }, signal: AbortSignal.timeout(5000) });
  assert.equal(page.status, 200);
  assert.match(await page.text(), /<html/i);
  const session = await fetch(`${base}/session`, {
    method: "POST", headers: { "Content-Type": "application/json", Origin: base, Connection: "close" },
    body: JSON.stringify({ password: "admin" }), signal: AbortSignal.timeout(5000),
  });
  assert.equal(session.status, 200, `first-boot dashboard login: ${await session.text()}`);
  const token = control("token-create", JSON.stringify({ name: "container-smoke" })).token;
  assert(token, "agent token is created");
  const headers = { Authorization: `Bearer ${token}`, Connection: "close" };
  const before = control("settings");
  control("add", "records", JSON.stringify({ revision: before.status.saved_revision, item: { name: "desktop.example", type: "A", value: "192.0.2.42", ttl: 30 } }));
  await checkDNS();
  // Persisted settings, secrets and statistics survive container replacement.
  const revision = control("settings").status.saved_revision;
  compose("exec", "-T", "dimsum", "sh", "-c", "printf preserved > /var/lib/dimsum/persistence-check");
  compose("up", "-d", "--force-recreate", "--wait", "--wait-timeout", "90", "--pull", "never");
  assert.equal(control("settings").status.saved_revision, revision);
  allowPublishedAdminPort();
  assert.equal(compose("exec", "-T", "dimsum", "cat", "/var/lib/dimsum/persistence-check"), "preserved");
  const authenticated = await fetch(`http://127.0.0.1:${port(8080)}/api/v1/dhcp`, { headers, signal: AbortSignal.timeout(5000) });
  assert.equal(authenticated.status, 200, "agent token survives recreation");
  assert.equal((await authenticated.json()).availability.code, "docker_desktop");
  const range = new URLSearchParams({ from: started, to: new Date().toISOString() });
  assert(Number(control("summary", "--query", range.toString()).queries) >= 2, "query history survives recreation");
  await checkDNS();
  console.log("Container smoke passed: first boot, login, DHCP capability, UDP/TCP DNS, saved configuration and token persistence, recreation.");
} catch (err) {
  console.error(compose("ps", "--format", "json"));
  try {
    console.error(JSON.stringify({ summary: control("summary"), records: control("records"), udpPort: port(53, "udp"), tcpPort: port(53) }, null, 2));
  } catch { /* Preserve the original failure if startup did not complete. */ }
  console.error(compose("logs", "--no-color", "--tail", "80"));
  throw err;
} finally {
  compose("down", "--volumes", "--remove-orphans");
}
