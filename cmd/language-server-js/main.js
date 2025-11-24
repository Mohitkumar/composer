#!/usr/bin/env node

/**
 * JavaScript/Node.js Language Worker
 * 
 * This worker exposes a gRPC service for executing JavaScript/Node.js code.
 * 
 * Usage:
 *   PORT=50052 node cmd/worker-js/main.js
 * 
 * Environment Variables:
 *   PORT - gRPC server port (default: 50052)
 */

const grpc = require('@grpc/grpc-js');
const protoLoader = require('@grpc/proto-loader');
const path = require('path');

// Load proto file - try multiple paths
let protoPath = path.join(__dirname, '../../pkg/api/proto/workflow_executor.proto');
if (!require('fs').existsSync(protoPath)) {
  // Try alternative path (for Docker)
  protoPath = path.join(__dirname, './proto/workflow_executor.proto');
}
const pkgDef = protoLoader.loadSync(protoPath, {
  keepCase: true,
  longs: String,
  enums: String,
  defaults: true,
  oneofs: true
});
const proto = grpc.loadPackageDefinition(pkgDef).workflow.exec;

/**
 * Execute handler for LanguageExecutor service
 */
function Execute(call, callback) {
  try {
    const req = call.request;
    
    if (!req.payload || !req.payload.inline_code) {
      callback(null, {
        success: false,
        error: 'Only inline_code payload is supported'
      });
      return;
    }
    
    const code = req.payload.inline_code.code;
    const filename = req.payload.inline_code.filename || 'handler.js';
    const inputJson = req.input_json || '{}';
    
    // Parse input
    let input;
    try {
      input = JSON.parse(inputJson);
    } catch (e) {
      callback(null, {
        success: false,
        error: `Invalid input_json: ${e.message}`
      });
      return;
    }
    
    // WARNING: This executes arbitrary code - use with caution in production
    // In production, consider using a sandboxed environment or VM
    const startTime = Date.now();
    
    try {
      // Create a function that executes the code
      // The code should define a handler function
      const fn = new Function('input', `
        ${code}
        return (typeof handler === 'function') ? handler(input) : null;
      `);
      
      const output = fn(input);
      const outputJson = JSON.stringify(output);
      const duration = Date.now() - startTime;
      
      callback(null, {
        success: true,
        output_json: outputJson,
        duration_ms: duration,
        logs: []
      });
    } catch (execErr) {
      callback(null, {
        success: false,
        error: execErr.toString(),
        duration_ms: Date.now() - startTime
      });
    }
  } catch (err) {
    callback(null, {
      success: false,
      error: err.toString()
    });
  }
}

/**
 * Main function to start the gRPC server
 */
function main() {
  const port = process.env.PORT || '50052';
  const server = new grpc.Server();
  
  server.addService(proto.LanguageExecutor.service, { Execute: Execute });
  
  server.bindAsync(
    `0.0.0.0:${port}`,
    grpc.ServerCredentials.createInsecure(),
    (err, bindPort) => {
      if (err) {
        console.error('Failed to start server:', err);
        process.exit(1);
      }
      server.start();
      console.log(`JavaScript language worker started on port ${bindPort}`);
    }
  );
}

// Handle graceful shutdown
process.on('SIGINT', () => {
  console.log('Shutting down...');
  process.exit(0);
});

process.on('SIGTERM', () => {
  console.log('Shutting down...');
  process.exit(0);
});

main();

