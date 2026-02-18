/**
 * Secure sandbox creation and management for JavaScript code execution.
 *
 * Uses isolated-vm (V8 isolates) instead of the deprecated vm2 library.
 * Each execution gets its own V8 isolate with hard memory limits and
 * no access to the host process, filesystem, network, or Node.js APIs.
 */

import ivm from 'isolated-vm';
import { DEFAULT_MEMORY_LIMIT_MB } from './config.js';

/**
 * Execute code inside an isolated V8 isolate.
 *
 * @param {string} code  - User code (must define a `handler` function)
 * @param {*}      input - Input data (JSON-serializable) passed to handler()
 * @param {number} timeoutMs - Execution timeout in milliseconds
 * @returns {*} The JSON-round-tripped return value of handler(input)
 */
function runInIsolate(code, input, timeoutMs) {
  // Create an isolate with a hard memory ceiling
  const isolate = new ivm.Isolate({ memoryLimit: DEFAULT_MEMORY_LIMIT_MB });

  try {
    const context = isolate.createContextSync();
    const jail = context.global;

    // Provide a minimal console.log that captures output (no host object leaks)
    const logs = [];
    jail.setSync('_log', new ivm.Callback((...args) => {
      logs.push(args.map(String).join(' '));
    }));

    // Inject a frozen copy of the input as a JSON string — the user code
    // parses it inside the isolate so no host references leak in.
    jail.setSync('_inputJSON', JSON.stringify(input));

    // Bootstrap: set up console, parse input, and define `input` global
    const bootstrap = `
      const console = {
        log:   (...a) => _log(...a),
        error: (...a) => _log('[error]', ...a),
        warn:  (...a) => _log('[warn]', ...a),
        info:  (...a) => _log(...a),
      };
      const input = JSON.parse(_inputJSON);
    `;

    // Compile and run bootstrap + user code + handler invocation
    const fullScript = `
      ${bootstrap}
      ${code}

      if (typeof handler !== 'function') {
        throw new Error('handler function is not defined or is not a function');
      }
      const _result = handler(input);
      JSON.stringify(_result);
    `;

    const script = isolate.compileScriptSync(fullScript);
    const resultJSON = script.runSync(context, { timeout: timeoutMs });

    // resultJSON is a string (JSON.stringify ran inside the isolate)
    return JSON.parse(resultJSON);
  } finally {
    // Tear down the isolate immediately to free memory
    isolate.dispose();
  }
}

export { runInIsolate };
