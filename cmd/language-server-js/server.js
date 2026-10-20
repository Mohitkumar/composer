/**
 * gRPC server setup and request handling
 */

import grpc from '@grpc/grpc-js';
import protoLoader from '@grpc/proto-loader';
import path from 'path';
import { fileURLToPath } from 'url';
import { existsSync } from 'fs';
import { executeCode, parseInput, serializeOutput, formatError } from './executor.js';
import { DEFAULT_PORT } from './config.js';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

/**
 * Load protobuf definitions
 * @returns {Object} Protobuf service definitions
 */
function loadProto() {
  // Load proto file - try multiple paths
  let protoPath = path.join(__dirname, '../../pkg/api/proto/workflow_executor.proto');
  if (!existsSync(protoPath)) {
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
async function Execute(call, callback) {
  const startTime = Date.now();
  
  try {
    const req = call.request;
    
    // With protoLoader and oneofs: true, oneof fields can be accessed directly
    // Try both possible structures: req.inline_code or req.payload.inline_code
    let inlineCode = req.inline_code;
    if (!inlineCode && req.payload) {
      inlineCode = req.payload.inline_code;
    }
    
    if (!inlineCode) {
      // Log for debugging
      console.error('Request structure:', JSON.stringify({
        hasInlineCode: !!req.inline_code,
        hasPayload: !!req.payload,
        hasPayloadInlineCode: !!(req.payload && req.payload.inline_code),
        requestKeys: Object.keys(req)
      }));
      callback(null, {
        success: false,
        error: 'Only inline_code payload is supported',
        duration_ms: Date.now() - startTime
      });
      return;
    }
    
    const code = inlineCode.code;
    const filename = inlineCode.filename || 'handler.js';
    const inputJson = req.input_json || '{}';
    const timeoutSeconds = req.timeout_seconds;
    
    // Log metadata only (not content) to avoid leaking sensitive data
    console.log(`[JS Executor] file=${filename} code_size=${code.length} input_size=${inputJson.length} timeout=${timeoutSeconds || 'default'}s`);
    
    // Parse input
    let input;
    try {
      input = parseInput(inputJson);
    } catch (e) {
      console.error(`[JS Executor] Failed to parse input:`, e.message);
      callback(null, {
        success: false,
        error: e.message,
        duration_ms: Date.now() - startTime
      });
      return;
    }
    
    // Execute code
    try {
      const { output, logs } = await executeCode(code, input, timeoutSeconds);
      callback(null, {
        success: true,
        output_json: serializeOutput(output),
        duration_ms: Date.now() - startTime,
        logs,
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
      // Note: server.start() is deprecated in @grpc/grpc-js >= 1.10;
      // the server begins accepting connections after bindAsync succeeds.
      console.log(`JavaScript language worker started on port ${bindPort}`);
    }
  );
  
  return server;
}

export {
  startServer,
  Execute
};

