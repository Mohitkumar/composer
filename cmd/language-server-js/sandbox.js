/**
 * Secure sandbox creation and management for JavaScript code execution
 */

import { VM } from 'vm2';

/**
 * Create a secure VM sandbox for executing user code
 * @param {number} timeoutMs - Execution timeout in milliseconds
 * @param {*} input - Input data to make available in the sandbox
 * @returns {VM} Configured VM instance
 */
function createSandbox(timeoutMs, input) {
  return new VM({
    timeout: timeoutMs,
    sandbox: {
      // Input data for the handler
      input: input,
      // Only provide safe built-ins
      console: {
        log: (...args) => {
          // Log to stdout so it can be captured
          console.log('[JS Code]', ...args);
        },
        error: (...args) => {
          // Log to stderr so it can be captured
          console.error('[JS Code]', ...args);
        },
        warn: (...args) => {
          console.warn('[JS Code]', ...args);
        },
        info: (...args) => {
          console.info('[JS Code]', ...args);
        }
      },
      // JSON utilities are safe
      JSON: JSON,
      // Math is safe
      Math: Math,
      // Date is safe (read-only access)
      Date: Date,
      // Number, String, Boolean constructors are safe
      Number: Number,
      String: String,
      Boolean: Boolean,
      Array: Array,
      Object: Object,
      // RegExp is safe
      RegExp: RegExp,
      // Error constructors are safe
      Error: Error,
      TypeError: TypeError,
      RangeError: RangeError,
      // Promise is safe
      Promise: Promise,
      // setTimeout/setInterval with limited functionality
      setTimeout: (fn, delay) => {
        // Only allow reasonable delays (max 1 second)
        const safeDelay = Math.min(Math.max(0, delay || 0), 1000);
        return setTimeout(fn, safeDelay);
      },
      clearTimeout: clearTimeout,
      setInterval: (fn, delay) => {
        const safeDelay = Math.min(Math.max(0, delay || 0), 1000);
        return setInterval(fn, safeDelay);
      },
      clearInterval: clearInterval
    },
    // Disable all Node.js built-in modules for security
    // User code cannot access: fs, child_process, http, https, net, etc.
    // Only allow safe operations
    eval: false, // Disable eval
    wasm: false, // Disable WebAssembly
    fixAsync: true // Fix async/await in sandbox
  });
}

export {
  createSandbox
};

