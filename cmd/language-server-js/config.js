/**
 * Configuration constants for the JavaScript language server
 */

// Default timeout: 30 seconds
const DEFAULT_TIMEOUT_MS = 30000;

// Memory limit per isolate: 128MB (enforced by isolated-vm)
const DEFAULT_MEMORY_LIMIT_MB = 128;

// Isolates running at once; further requests wait. Bounds host memory and CPU.
const MAX_CONCURRENT_ISOLATES = Number(process.env.MAX_CONCURRENT_ISOLATES) || 4;

// Console output kept per execution: 64KB
const MAX_LOG_BYTES = 64 * 1024;

// Maximum size of a handler's JSON output: 10MB
const MAX_OUTPUT_BYTES = 10 * 1024 * 1024;

// Maximum code size: 1MB (prevent DoS)
const MAX_CODE_SIZE = 1024 * 1024;

// Default server port
const DEFAULT_PORT = '50052';

export {
  DEFAULT_TIMEOUT_MS,
  DEFAULT_MEMORY_LIMIT_MB,
  MAX_CONCURRENT_ISOLATES,
  MAX_LOG_BYTES,
  MAX_OUTPUT_BYTES,
  MAX_CODE_SIZE,
  DEFAULT_PORT
};

