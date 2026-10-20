/**
 * Code execution logic for JavaScript language server.
 *
 * Uses isolated-vm for true V8 isolate-level sandboxing with hard memory
 * limits and timeout enforcement.
 */

import { runInIsolate } from './sandbox.js';
import { DEFAULT_TIMEOUT_MS, MAX_CODE_SIZE } from './config.js';

/**
 * Validate and parse input JSON
 * @param {string} inputJson - JSON string input
 * @returns {*} Parsed input object
 * @throws {Error} If JSON is invalid
 */
function parseInput(inputJson) {
  try {
    return JSON.parse(inputJson || '{}');
  } catch (e) {
    throw new Error(`Invalid input_json: ${e.message}`);
  }
}

/**
 * Validate code size
 * @param {string} code - Code to validate
 * @throws {Error} If code exceeds maximum size
 */
function validateCodeSize(code) {
  if (code.length > MAX_CODE_SIZE) {
    throw new Error(`Code size exceeds maximum allowed size of ${MAX_CODE_SIZE} bytes`);
  }
}

/**
 * Calculate timeout in milliseconds, capped at DEFAULT_TIMEOUT_MS.
 * @param {number} timeoutSeconds - Timeout in seconds from request
 * @returns {number} Timeout in milliseconds
 */
function calculateTimeout(timeoutSeconds) {
  const timeout = timeoutSeconds || (DEFAULT_TIMEOUT_MS / 1000);
  return Math.min(timeout * 1000, DEFAULT_TIMEOUT_MS);
}

/**
 * Execute JavaScript code in a secure isolate.
 * @param {string} code  - User code (must define a `handler` function)
 * @param {*}      input - Input data for the handler
 * @param {number} timeoutSeconds - Timeout in seconds
 * @returns {Promise<{output: *, logs: string[]}>} handler(input) and console output
 * @throws {Error} If execution fails or times out
 */
async function executeCode(code, input, timeoutSeconds) {
  validateCodeSize(code);
  const timeoutMs = calculateTimeout(timeoutSeconds);
  return runInIsolate(code, input, timeoutMs);
}

/**
 * Serialize output to JSON
 * @param {*} output - Output to serialize
 * @returns {string} JSON string
 * @throws {Error} If serialization fails
 */
function serializeOutput(output) {
  try {
    return JSON.stringify(output);
  } catch (e) {
    throw new Error(`Failed to serialize output: ${e.message}. Output must be JSON-serializable.`);
  }
}

/**
 * Format error message, handling timeouts specifically
 * @param {Error} error - Error object
 * @param {number} timeoutSeconds - Timeout in seconds
 * @returns {string} Formatted error message
 */
function formatError(error, timeoutSeconds) {
  const msg = error.message || error.toString();
  if (msg.includes('Script execution timed out') || msg.includes('timed out')) {
    return `Execution timed out after ${timeoutSeconds} seconds`;
  }
  if (msg.includes('exceeded the memory limit') || msg.includes('Array buffer allocation failed')) {
    return 'Code exceeded memory limit';
  }
  return msg;
}

export {
  executeCode,
  parseInput,
  serializeOutput,
  formatError
};
