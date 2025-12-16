/**
 * Configuration constants for the JavaScript language server
 */

// Default timeout: 30 seconds
const DEFAULT_TIMEOUT_MS = 30000;

// Default memory limit: 128MB (for reference, not enforced by vm2)
const DEFAULT_MEMORY_LIMIT_MB = 128;

// Maximum code size: 1MB (prevent DoS)
const MAX_CODE_SIZE = 1024 * 1024;

// Default server port
const DEFAULT_PORT = '50052';

module.exports = {
  DEFAULT_TIMEOUT_MS,
  DEFAULT_MEMORY_LIMIT_MB,
  MAX_CODE_SIZE,
  DEFAULT_PORT
};

