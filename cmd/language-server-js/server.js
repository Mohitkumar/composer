/**
 * gRPC server setup and request handling
 */

const grpc = require('@grpc/grpc-js');
const protoLoader = require('@grpc/proto-loader');
const path = require('path');
const { executeCode, parseInput, serializeOutput, formatError } = require('./executor');
const { DEFAULT_PORT } = require('./config');

/**
 * Load protobuf definitions
 * @returns {Object} Protobuf service definitions
 */
function loadProto() {
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
  
  return grpc.loadPackageDefinition(pkgDef).workflow.exec;
}

/**
 * Execute handler for LanguageExecutor service
 * @param {Object} call - gRPC call object
 * @param {Function} callback - gRPC callback function
 */
function Execute(call, callback) {
  const startTime = Date.now();
  
  try {
    const req = call.request;
    
    // Validate payload type
    if (!req.payload || !req.payload.inline_code) {
      callback(null, {
        success: false,
        error: 'Only inline_code payload is supported',
        duration_ms: Date.now() - startTime
      });
      return;
    }
    
    const code = req.payload.inline_code.code;
    const filename = req.payload.inline_code.filename || 'handler.js';
    const inputJson = req.input_json || '{}';
    const timeoutSeconds = req.timeout_seconds;
    
    // Parse input
    let input;
    try {
      input = parseInput(inputJson);
    } catch (e) {
      callback(null, {
        success: false,
        error: e.message,
        duration_ms: Date.now() - startTime
      });
      return;
    }
    
    // Execute code
    try {
      const output = executeCode(code, input, timeoutSeconds);
      const outputJson = serializeOutput(output);
      const duration = Date.now() - startTime;
      
      callback(null, {
        success: true,
        output_json: outputJson,
        duration_ms: duration,
        logs: []
      });
    } catch (execErr) {
      const errorMsg = formatError(execErr, timeoutSeconds || (30));
      
      callback(null, {
        success: false,
        error: errorMsg,
        duration_ms: Date.now() - startTime
      });
    }
  } catch (err) {
    callback(null, {
      success: false,
      error: err.toString(),
      duration_ms: Date.now() - startTime
    });
  }
}

/**
 * Start the gRPC server
 * @param {string} port - Port to listen on
 */
function startServer(port) {
  const proto = loadProto();
  const server = new grpc.Server();
  
  server.addService(proto.LanguageExecutor.service, { Execute });
  
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
  
  return server;
}

module.exports = {
  startServer,
  Execute
};

