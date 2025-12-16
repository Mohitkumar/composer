#!/usr/bin/env node

/**
 * JavaScript/Node.js Language Worker
 * 
 * This worker exposes a gRPC service for executing JavaScript/Node.js code.
 * 
 * Usage:
 *   PORT=50052 node cmd/language-server-js/main.js
 * 
 * Environment Variables:
 *   PORT - gRPC server port (default: 50052)
 */

const { startServer } = require('./server');
const { DEFAULT_PORT } = require('./config');

// Handle graceful shutdown
process.on('SIGINT', () => {
  console.log('Shutting down...');
  process.exit(0);
});

process.on('SIGTERM', () => {
  console.log('Shutting down...');
  process.exit(0);
});

// Start server
const port = process.env.PORT || DEFAULT_PORT;
startServer(port);
