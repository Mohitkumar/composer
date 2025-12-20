/**
 * Code execution logic for JavaScript language server
 */

import { createSandbox } from './sandbox.js';
import { DEFAULT_TIMEOUT_MS, MAX_CODE_SIZE } from './config.js';

/**
 * Wrap user code with handler validation and execution
 * @param {string} code - User-provided code
 * @returns {string} Wrapped code ready for execution
 */
function wrapCode(code) {
  return `
    ${code}
    
    // Log input for debugging
    console.log('Input received in handler:', JSON.stringify(input, null, 2));
    console.log('Input type:', typeof input);
    
    // Ensure handler function exists
    if (typeof handler !== 'function') {
      throw new Error('handler function is not defined or is not a function');
    }
    
    console.log('Handler function found, calling handler(input)...');
    
    // Execute handler with input
    const result = handler(input);
    
    console.log('Handler returned result:', typeof result);
    console.log('Result preview:', JSON.stringify(result, null, 2).substring(0, 200));
    
    result;
  `;
}

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
 * Calculate timeout in milliseconds
 * @param {number} timeoutSeconds - Timeout in seconds from request
 * @returns {number} Timeout in milliseconds
 */
function calculateTimeout(timeoutSeconds) {
  const timeout = timeoutSeconds || (DEFAULT_TIMEOUT_MS / 1000);
  return Math.min(timeout * 1000, DEFAULT_TIMEOUT_MS);
}

/**
 * Execute JavaScript code in a secure sandbox
 * @param {string} code - User code to execute
 * @param {*} input - Input data for the handler
 * @param {number} timeoutSeconds - Timeout in seconds
 * @returns {*} Execution result
 * @throws {Error} If execution fails or times out
 */
function executeCode(code, input, timeoutSeconds) {
  // Validate code size
  validateCodeSize(code);
  
  // Calculate timeout
  const timeoutMs = calculateTimeout(timeoutSeconds);
  
  // Log input being passed to sandbox
  console.log(`[JS Executor] Creating sandbox with input:`, JSON.stringify(input, null, 2));
  console.log(`[JS Executor] Input will be available as 'input' variable in sandbox`);
  
  // Create secure sandbox
  const vm = createSandbox(timeoutMs, input);
  
  // Wrap code with handler validation
  const wrappedCode = wrapCode(code);
  
  console.log(`[JS Executor] Executing wrapped code...`);
  console.log(`[JS Executor] Wrapped code preview (first 200 chars):`, wrappedCode.substring(0, 200));
  
  // Execute in sandbox
  const output = vm.run(wrappedCode);
  
  console.log(`[JS Executor] Execution completed. Output type:`, typeof output);
  console.log(`[JS Executor] Output preview:`, JSON.stringify(output, null, 2).substring(0, 500));
  
  return output;
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
  const errorMsg = error.toString();
  if (errorMsg.includes('Script execution timed out') || errorMsg.includes('timeout')) {
    return `Execution timed out after ${timeoutSeconds} seconds`;
  }
  return errorMsg;
}

export {
  executeCode,
  parseInput,
  serializeOutput,
  formatError
};

