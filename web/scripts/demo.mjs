// Starts a local Tollgate with demo data for the dashboard: two fake
// providers with a simulated prompt cache, a fake provider that always
// fails, a usage file with 7 days of synthetic history, and live requests
// from agent-like sessions. Run it with "make dashboard-demo", then open
// http://127.0.0.1:9090. Stop it with Ctrl-C.
//
// All data is synthetic. Do not use the demo key anywhere else.

import { spawn } from 'node:child_process';
import { createHash, randomBytes } from 'node:crypto';
import { mkdtempSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';

const root = resolve(import.meta.dirname, '../..');
const bin = join(root, 'bin');
const dir = mkdtempSync(join(tmpdir(), 'tollgate-dashboard-'));
const usagePath = join(dir, 'usage.jsonl');

// mulberry32 is a small seeded random number generator, so the history is
// the same in each run.
function mulberry32(seed) {
  return () => {
    seed = (seed + 0x6d2b79f5) | 0;
    let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}
const rand = mulberry32(2026);
const pick = (a) => a[Math.floor(rand() * a.length)];

// Prices of claude-sonnet-5-5 and claude-haiku-5-5 in pricing.yaml, in
// micro-USD for each million tokens.
const prices = {
  'claude-sonnet-5-5': { input: 2_000_000, output: 10_000_000, read: 100_000, write: 2_500_000 },
  'claude-haiku-5-5': { input: 1_000_000, output: 5_000_000, read: 100_000, write: 1_250_000 },
};
const cost = (m, u) =>
  Math.ceil((u.input * prices[m].input + u.output * prices[m].output + u.read * prices[m].read + u.write * prices[m].write) / 1e6);

const keys = {
  platform: ['ana-laptop', 'raj-laptop', 'ci'],
  research: ['notebook'],
};

// history writes the synthetic events of the last 7 days.
function history(now) {
  const lines = [];
  const start = now - 7 * 86400_000;
  let id = 0;
  for (let t = start; t < now - 20 * 60_000; ) {
    const hour = new Date(t).getUTCHours();
    const busy = hour >= 13 && hour <= 23 ? 1 : 0.2;
    t += (6 + rand() * 40) * 60_000 / busy;
    const tenant = rand() < 0.75 ? 'platform' : 'research';
    const key = pick(keys[tenant]);
    const derived = rand() < 0.4;
    const session = derived ? `auto-${randomBytes(16).toString('hex')}` : `${key}-${Math.floor(t / 1000).toString(36)}`;
    const model = rand() < 0.85 ? 'claude-sonnet-5-5' : 'claude-haiku-5-5';
    const turns = 4 + Math.floor(rand() * 40);
    let prompt = 4000 + Math.floor(rand() * 6000);
    let ts = t;
    let target = 'fake-primary';
    for (let i = 0; i < turns; i++) {
      ts += (8 + rand() * 50) * 1000;
      const add = 300 + Math.floor(rand() * 2500);
      let status = 200;
      let attempts = 1;
      let switched = false;
      const r = rand();
      if (model === 'claude-haiku-5-5') {
        // The first target of the haiku route always fails.
        target = 'fake-backup';
        attempts = 2;
      } else if (r < 0.025) {
        attempts = 2;
        if (target === 'fake-primary') {
          target = 'fake-backup';
          switched = true;
        }
      } else if (r < 0.035) status = 429;
      else if (r < 0.04) status = 502;
      const output = status === 200 ? 80 + Math.floor(rand() * 900) : 0;
      const u = status === 200 ? { input: 3, read: switched || i === 0 ? 0 : prompt, write: switched || i === 0 ? prompt + add : add, output } : { input: 0, read: 0, write: 0, output: 0 };
      const c = status === 200 ? cost(model, u) : 0;
      const loss = switched ? Math.max(cost(model, { input: u.input, read: 0, write: u.write, output: 0 }) - cost(model, { input: 0, read: u.input + u.write, write: 0, output: 0 }), 0) : 0;
      const ttft = status === 200 ? 300 + Math.floor(rand() * (target === 'fake-primary' ? 600 : 1200)) : null;
      lines.push(
        JSON.stringify({
          request_id: `SEED${(id++).toString().padStart(8, '0')}`,
          ts: new Date(ts).toISOString(),
          tenant_id: tenant,
          key_id: key,
          session_id: session,
          route_model: model,
          provider: status === 429 ? '' : target,
          provider_model: status === 429 ? '' : model,
          status,
          attempts: status === 429 ? 0 : attempts,
          cache: 'bypass',
          input_tokens: u.input,
          output_tokens: u.output,
          cache_read_tokens: u.read,
          cache_write_tokens: u.write,
          cache_write_1h_tokens: 0,
          reasoning_tokens: 0,
          usage_estimated: false,
          cost_micro_usd: c,
          ttft_ms: ttft,
          duration_ms: (ttft ?? 0) + output * 12,
          overhead_us: 120 + Math.floor(rand() * 400),
          session_derived: derived || undefined,
          target_switched: switched || undefined,
          cache_loss_micro_usd: loss || undefined,
        }),
      );
      if (status === 200) prompt += add + output;
      if (model !== 'claude-haiku-5-5' && rand() < 0.15 && target === 'fake-backup') target = 'fake-primary';
    }
  }
  writeFileSync(usagePath, lines.join('\n') + '\n');
  return lines.length;
}

function keyPair() {
  const alphabet = '0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz';
  let k = 'tg_';
  for (const b of randomBytes(43)) k += alphabet[b % 62];
  return { key: k, hash: `sha256:${createHash('sha256').update(k).digest('hex')}` };
}

const live = { platform: keyPair(), research: keyPair() };
const config = `version: 1
server:
  listen: "127.0.0.1:8080"
  admin_listen: "127.0.0.1:9090"
usage:
  path: ${usagePath}
breaker:
  min_requests: 5
  open_for: 30s
providers:
  fake-primary: {kind: anthropic, base_url: "http://127.0.0.1:8091"}
  fake-backup: {kind: anthropic, base_url: "http://127.0.0.1:8092"}
  fake-flaky: {kind: anthropic, base_url: "http://127.0.0.1:8093"}
routes:
  - model: claude-sonnet-5-5
    api: anthropic
    targets: [{provider: fake-primary}, {provider: fake-backup}]
  - model: claude-haiku-5-5
    api: anthropic
    targets: [{provider: fake-flaky}, {provider: fake-backup}]
    response_cache: {enabled: true, ttl: 10m}
tenants:
  - id: platform
    keys:
      - {id: ana-laptop, hash: "${live.platform.hash}"}
      - {id: raj-laptop, hash: "sha256:${'1'.repeat(64)}"}
      - {id: ci, hash: "sha256:${'2'.repeat(64)}"}
    models: [claude-sonnet-5-5, claude-haiku-5-5]
    limits: {requests_per_minute: 600}
    budgets:
      - {window: month, limit_usd: 150}
      - {window: session, limit_usd: 2, mode: hard}
    redact: {kinds: [email, credit_card]}
  - id: research
    keys: [{id: notebook, hash: "${live.research.hash}"}]
    models: [claude-sonnet-5-5, claude-haiku-5-5]
    budgets:
      - {window: month, limit_usd: 40}
`;
writeFileSync(join(dir, 'tollgate.yaml'), config);

const children = [];
function start(name, args) {
  const c = spawn(join(bin, name), args, { stdio: ['ignore', 'ignore', 'inherit'] });
  children.push(c);
  return c;
}
process.on('SIGINT', () => {
  for (const c of children) c.kill('SIGTERM');
  process.exit(0);
});

const seeded = history(Date.now());
start('fakeprovider', ['-listen', '127.0.0.1:8091', '-prompt-cache', '5m', '-first-token-delay', '350ms', '-token-interval', '8ms']);
start('fakeprovider', ['-listen', '127.0.0.1:8092', '-prompt-cache', '5m', '-first-token-delay', '700ms', '-token-interval', '12ms']);
start('fakeprovider', ['-listen', '127.0.0.1:8093', '-scenario', 'status:529']);
await new Promise((r) => setTimeout(r, 500));
start('tollgate', ['serve', '-config', join(dir, 'tollgate.yaml')]);
await new Promise((r) => setTimeout(r, 1000));
console.log(`Tollgate demo: ${seeded} seeded events in ${usagePath}`);
console.log('Dashboard: http://127.0.0.1:9090   Data plane: http://127.0.0.1:8080   Stop: Ctrl-C');

// agent runs one agent-like session: each request has the conversation so
// far, with a growing prompt.
async function agent(n) {
  const tenant = n % 3 === 2 ? 'research' : 'platform';
  const named = n % 2 === 0;
  const session = `live-${n}`;
  const system = 'You are a coding agent. '.repeat(400);
  const messages = [{ role: 'user', content: `Task ${n}: fix the failing test in package ${pick(['server', 'router', 'limiter'])}. Mail the result to ana@example.com.` }];
  for (let turn = 0; turn < 30; turn++) {
    const model = turn % 7 === 6 ? 'claude-haiku-5-5' : 'claude-sonnet-5-5';
    const headers = { 'content-type': 'application/json', 'anthropic-version': '2023-06-01', 'x-api-key': live[tenant].key };
    if (named) headers['x-tollgate-session'] = session;
    try {
      const resp = await fetch('http://127.0.0.1:8080/v1/messages', {
        method: 'POST',
        headers,
        body: JSON.stringify({ model, max_tokens: 1024, stream: true, system, messages }),
      });
      await resp.text();
    } catch {
      return;
    }
    messages.push({ role: 'assistant', content: 'I read the file and ran the tests. '.repeat(10) });
    messages.push({ role: 'user', content: `Tool result ${turn}: ${'ok '.repeat(200 + Math.floor(rand() * 900))}` });
    await new Promise((r) => setTimeout(r, 1500 + rand() * 4000));
  }
}

// burst sends quick haiku requests. The first target of the haiku route
// always fails, so its circuit breaker opens. Requests with the same body
// are hits in the response cache of the route.
async function burst(n) {
  for (let i = 0; i < 8; i++) {
    const body = { model: 'claude-haiku-5-5', max_tokens: 64, messages: [{ role: 'user', content: i < 6 ? `Name this file ${n}-${i}.` : 'Name this file.' }] };
    try {
      await (await fetch('http://127.0.0.1:8080/v1/messages', {
        method: 'POST',
        headers: { 'content-type': 'application/json', 'anthropic-version': '2023-06-01', 'x-api-key': live.platform.key },
        body: JSON.stringify(body),
      })).text();
    } catch {
      return;
    }
  }
}

for (let n = 0; ; n++) {
  void agent(n);
  if (n % 3 === 0) void burst(n);
  await new Promise((r) => setTimeout(r, 8000 + rand() * 8000));
}
