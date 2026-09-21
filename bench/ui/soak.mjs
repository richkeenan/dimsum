// Real managed-service live-view observation. Run after building the current binary:
// node bench/ui/soak.mjs /absolute/dimsum /new/evidence/directory 3600
import { createRequire } from 'node:module';
import { spawn, execFileSync } from 'node:child_process';
import { mkdir, writeFile, appendFile } from 'node:fs/promises';
import { resolve, join } from 'node:path';
import { createSocket } from 'node:dgram';
import { once } from 'node:events';
import { createInterface } from 'node:readline';
import { setTimeout as delay } from 'node:timers/promises';

const require = createRequire(new URL('../../web/package.json', import.meta.url));
const { chromium } = require('@playwright/test');
const [binary, directory, secondsText = '3600'] = process.argv.slice(2);
const seconds = Number(secondsText);
if (!binary || !directory || !Number.isFinite(seconds) || seconds < 1 || seconds > 86400) throw Error('binary, new directory, duration 1..86400 required');
const dir = resolve(directory);
await mkdir(dir, { mode: 0o700 });
const config = join(dir, 'dimsum.yaml');
const password = 'isolated-live-view-fixture-password';
await writeFile(config, `version: 1\ndns:\n  listen: [127.0.0.1:0]\n  upstreams: [127.0.0.1:9]\nadmin:\n  listen: 127.0.0.1:0\npaths:\n  data_dir: data\n  secrets_dir: secrets\nrules:\n  - id: fixture\n    action: deny\n    kind: suffix\n    pattern: fixture.test\n    enabled: true\n`, { mode: 0o600 });
execFileSync(binary, ['bootstrap', '-config', config, '-password-file', '-'], { input: password, stdio: ['pipe', 'pipe', 'pipe'] });
const daemon = spawn(binary, ['serve', '-config', config], { stdio: ['ignore', 'pipe', 'pipe'] });
let daemonError = '';
daemon.stderr.on('data', b => { daemonError = (daemonError + b).slice(-8192); });
const lines = createInterface({ input: daemon.stdout });
let browser;
let socket;
const report = { duration_requested_s: seconds, completed: false, queries: 0, samples: 0, errors: [] };
try {
  const first = await Promise.race([once(lines, 'line'), delay(10000).then(() => { throw Error('startup timeout: ' + daemonError); })]);
  const startup = JSON.parse(first[0]);
  const address = new URL('udp://' + startup.dns[0]);
  socket = createSocket('udp4');
  const query = Buffer.from([0, 1, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 7, ...Buffer.from('fixture'), 4, ...Buffer.from('test'), 0, 0, 1, 0, 1]);
  async function dns() {
    const reply = once(socket, 'message');
    socket.send(query, Number(address.port), address.hostname);
    const [wire] = await Promise.race([reply, delay(1000).then(() => { throw Error('DNS timeout'); })]);
    if (wire.readUInt16BE(6) !== 1 || (wire[3] & 15) !== 0) throw Error('unexpected blocked reply');
    report.queries++;
  }
  browser = await chromium.launch({ args: ['--enable-precise-memory-info'] });
  const page = await browser.newPage();
  page.on('pageerror', error => { if (report.errors.length < 100) report.errors.push(String(error)); });
  await page.goto('http://' + startup.admin);
  await page.getByLabel('Admin password').fill(password);
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await page.getByRole('heading', { name: 'Overview', exact: true }).waitFor();
  const cdp = await page.context().newCDPSession(page);
  await cdp.send('Performance.enable');
  const start = performance.now();
  let nextSample = 0;
  while (performance.now() - start < seconds * 1000) {
    await dns();
    const elapsed = (performance.now() - start) / 1000;
    if (elapsed >= nextSample) {
      await page.getByRole('button', { name: 'Refresh all data' }).click();
      const metrics = Object.fromEntries((await cdp.send('Performance.getMetrics')).metrics.map(m => [m.name, m.value]));
      const rss = Number(execFileSync('ps', ['-o', 'rss=', '-p', String(daemon.pid)], { encoding: 'utf8' }).trim()) * 1024;
      await appendFile(join(dir, 'memory.jsonl'), JSON.stringify({ elapsed_s: elapsed, daemon_rss_bytes: rss, js_heap_bytes: metrics.JSHeapUsedSize, dom_nodes: metrics.Nodes, documents: metrics.Documents }) + '\n');
      report.samples++;
      nextSample = elapsed + 5;
    }
    await delay(100);
  }
  report.elapsed_s = (performance.now() - start) / 1000;
  report.completed = report.errors.length === 0;
  await page.screenshot({ path: join(dir, 'final.png'), fullPage: true });
} catch (error) {
  report.errors.push(String(error));
  process.exitCode = 1;
} finally {
  socket?.close();
  await browser?.close();
  daemon.kill('SIGTERM');
  await Promise.race([once(daemon, 'exit'), delay(5000).then(() => daemon.kill('SIGKILL'))]);
  report.daemon_stderr = daemonError;
  await writeFile(join(dir, 'report.json'), JSON.stringify(report, null, 2));
  console.log(JSON.stringify(report, null, 2));
}
