/**
 * Code execution logic for JavaScript language server
 */

const { createSandbox } = require('./sandbox');
const { DEFAULT_TIMEOUT_MS, MAX_CODE_SIZE } = require('./config');

/**
 * Wrap user code with handler validation and execution
 * @param {string} code - User-provided code
 * @returns {string} Wrapped code ready for execution
 */
function wrapCode(code) {
  return `
    ${code}
    
    // Ensure handler function exists
    if (typeof handler !== 'function') {
      throw new Error('handler function is not defined or is not a function');
    }
    
    // Execute handler with input
    const result = handler(input);
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
  
  // Create secure sandbox
  const vm = createSandbox(timeoutMs, input);
  
  // Wrap code with handler validation
  const wrappedCode = wrapCode(code);
  
  // Execute in sandbox
  const output = vm.run(wrappedCode);
  
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

module.exports = {
  executeCode,
  parseInput,
  serializeOutput,
  formatError
};

