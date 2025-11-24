const grpc = require('@grpc/grpc-js');
const protoLoader = require('@grpc/proto-loader');
const pkgDef = protoLoader.loadSync('proto/workflow_executor.proto', {keepCase: true, longs: String, enums: String, defaults: true, oneofs: true});
const proto = grpc.loadPackageDefinition(pkgDef).workflow.exec;
const util = require('util');

function Execute(call, callback) {
  try {
    const req = call.request;
    if (req.payload && req.payload.inline_code) {
      const code = req.payload.inline_code.code;
      // DANGEROUS: do not eval in prod.
      // For demo we execute JS code in a new function.
      const fn = new Function('input', code + '\nreturn (typeof handler === "function") ? handler(input) : null;');
      const input = req.input_json ? JSON.parse(req.input_json) : {};
      const out = fn(input);
      const outJson = JSON.stringify(out);
      callback(null, { success: true, output_json: outJson, duration_ms: 0, logs: [] });
      return;
    }
    callback(null, { success: false, error: 'payload not supported' });
  } catch (err) {
    callback(null, { success: false, error: err.toString() });
  }
}

function main() {
  const server = new grpc.Server();
  server.addService(proto.LanguageExecutor.service, { Execute: Execute });
  const port = process.env.PORT || '50052';
  server.bindAsync(`0.0.0.0:${port}`, grpc.ServerCredentials.createInsecure(), (err, bindPort) => {
    if (err) throw err;
    server.start();
    console.log('Node language worker started on', bindPort);
  });
}

main();
