/**
 * Secure sandbox creation and management for JavaScript code execution.
 *
 * Uses isolated-vm (V8 isolates). Each execution gets its own isolate with a
 * hard memory limit and no access to the host process, filesystem, network,
 * or Node.js APIs. Scripts run asynchronously on isolated-vm's thread pool, so
 * a long-running script never blocks the server's event loop, and at most
 * MAX_CONCURRENT_ISOLATES run at once. Everything that crosses back into the
 * host (console output, the result) is size-capped.
 */

import ivm from 'isolated-vm';
import {
  DEFAULT_MEMORY_LIMIT_MB,
  MAX_CONCURRENT_ISOLATES,
  MAX_LOG_BYTES,
  MAX_OUTPUT_BYTES,
} from './config.js';

/** A counting semaphore limiting concurrent isolates. */
class Limiter {
  constructor(max) {
    this.free = max;
    this.waiting = [];
  }

  async acquire() {
    if (this.free > 0) {
      this.free--;
      return;
    }
    await new Promise((resolve) => this.waiting.push(resolve));
  }

  release() {
    const next = this.waiting.shift();
    if (next) {
      next();
    } else {
      this.free++;
    }
  }
}

const limiter = new Limiter(MAX_CONCURRENT_ISOLATES);

/** Collects console output up to MAX_LOG_BYTES, then drops the rest. */
function logCollector() {
  const lines = [];
  let bytes = 0;
  let truncated = false;
  return {
    push(line) {
      if (bytes + line.length > MAX_LOG_BYTES) {
        truncated = true;
        return;
      }
      bytes += line.length;
      lines.push(line);
    },
    lines() {
      return truncated ? [...lines, '[log output truncated]'] : lines;
    },
  };
}

/**
 * Execute code inside an isolated V8 isolate.
 *
 * @param {string} code  - User code (must define a `handler` function)
 * @param {*}      input - Input data (JSON-serializable) passed to handler()
 * @param {number} timeoutMs - Execution timeout in milliseconds
 * @returns {Promise<{output: *, logs: string[]}>} handler(input), JSON-round-tripped, and console output
 */
async function runInIsolate(code, input, timeoutMs) {
  await limiter.acquire();
  const isolate = new ivm.Isolate({ memoryLimit: DEFAULT_MEMORY_LIMIT_MB });
  const logs = logCollector();

  try {
    const context = await isolate.createContext();
    const jail = context.global;

    // Only primitives cross this boundary: the callback receives copies.
    await jail.set('_log', new ivm.Callback((...args) => {
      logs.push(args.map(String).join(' '));
    }));
    // The input enters as a JSON string and is parsed inside the isolate, so
    // no host object reference leaks in.
    await jail.set('_inputJSON', JSON.stringify(input));

    const script = await isolate.compileScript(`
      const console = (() => {
        const log = _log;
        const write = (prefix) => (...a) => log(prefix, ...a.map((v) => {
          try { return typeof v === 'string' ? v : JSON.stringify(v); } catch { return String(v); }
        }));
        return { log: write(''), info: write(''), warn: write('[warn]'), error: write('[error]') };
      })();
      const input = JSON.parse(_inputJSON);
      delete globalThis._log;
      delete globalThis._inputJSON;

      ${code}

      if (typeof handler !== 'function') {
        throw new Error('handler function is not defined or is not a function');
      }
      JSON.stringify(handler(input));
    `);

    const resultJSON = await script.run(context, { timeout: timeoutMs });
    if (typeof resultJSON !== 'string') {
      throw new Error('handler must return a JSON-serializable value');
    }
    if (resultJSON.length > MAX_OUTPUT_BYTES) {
      throw new Error(`Output is ${resultJSON.length} bytes; the limit is ${MAX_OUTPUT_BYTES}`);
    }
    return { output: JSON.parse(resultJSON), logs: logs.lines() };
  } finally {
    isolate.dispose();
    limiter.release();
  }
}

export { runInIsolate };
