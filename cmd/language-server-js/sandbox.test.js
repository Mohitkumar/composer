/**
 * Sandbox regression tests. Run with: npm test
 */
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { executeCode } from './executor.js';

test('ordinary handler code works and console output is returned', async () => {
  const { output, logs } = await executeCode(`
    function handler(input) {
      console.log('summing', input.xs.length);
      return { sum: input.xs.reduce((a, b) => a + b, 0) };
    }`, { xs: [1, 2, 3] }, 5);
  assert.deepEqual(output, { sum: 6 });
  assert.deepEqual(logs, [' summing 3']);
});

test('host capabilities are not reachable', async () => {
  // Each expression must either throw inside the isolate or be undefined.
  for (const expr of ['require', 'process', 'globalThis._log', 'globalThis._inputJSON', 'setTimeout']) {
    let reachable;
    try {
      ({ output: reachable } = await executeCode(
        `function handler() { return typeof ${expr} !== 'undefined'; }`, {}, 5));
    } catch {
      reachable = false;
    }
    assert.equal(reachable, false, `${expr} is reachable from the sandbox`);
  }
});

test('runaway code times out without blocking the event loop', async () => {
  let ticks = 0;
  const timer = setInterval(() => ticks++, 50);
  await assert.rejects(executeCode('function handler() { while (true) {} }', {}, 1), /timed out/);
  clearInterval(timer);
  assert.ok(ticks >= 5, `event loop was blocked (only ${ticks} ticks)`);
});

test('memory and output are bounded', async () => {
  await assert.rejects(executeCode(`
    function handler() { const a = []; while (true) a.push('x'.repeat(1e6)); }`, {}, 10));
  await assert.rejects(
    executeCode(`function handler() { return 'x'.repeat(11 * 1024 * 1024); }`, {}, 10),
    /limit/,
  );
});

test('console output is capped', async () => {
  const { logs } = await executeCode(`
    function handler() { for (let i = 0; i < 100000; i++) console.log('line', i); return 1; }`, {}, 10);
  assert.equal(logs.at(-1), '[log output truncated]');
  assert.ok(logs.join('').length <= 70 * 1024);
});
