import time
import json
from concurrent import futures
import grpc
import logging

import workflow_executor_pb2 as pb
import workflow_executor_pb2_grpc as pb_grpc

_ONE_DAY_IN_SECONDS = 60 * 60 * 24

class LanguageExecutorServicer(pb_grpc.LanguageExecutorServicer):
    def Execute(self, request, context):
        start = time.time()
        logs = []
        try:
            runtime = request.runtime
            # For safety: DO NOT exec arbitrary code in production.
            # This demo will handle simple "inline python" that expects a function `handler(input)` and will echo.
            if request.payload.WhichOneof("payload") == "inline_code":
                code = request.payload.inline_code.code
                filename = request.payload.inline_code.filename
                # DEMO: do NOT exec untrusted code in prod.
                # We'll run code in a very limited local namespace for demo only.
                local_ns = {}
                try:
                    exec(code, {}, local_ns)
                    # Expect handler function
                    if "handler" in local_ns and callable(local_ns["handler"]):
                        input_json = json.loads(request.input_json) if request.input_json else {}
                        out = local_ns["handler"](input_json)
                        out_json = json.dumps(out)
                        duration = int((time.time() - start) * 1000)
                        return pb.ExecuteResponse(success=True, output_json=out_json, duration_ms=duration, logs=logs)
                    else:
                        return pb.ExecuteResponse(success=False, error="no handler() defined in code")
                except Exception as e:
                    return pb.ExecuteResponse(success=False, error=str(e))
            else:
                # other payload types: named tasks, entrypoints (not implemented here)
                return pb.ExecuteResponse(success=False, error="payload type not supported in demo")
        except Exception as e:
            return pb.ExecuteResponse(success=False, error=str(e))

def serve(port=50051):
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=4))
    pb_grpc.add_LanguageExecutorServicer_to_server(LanguageExecutorServicer(), server)
    server.add_insecure_port(f"[::]:{port}")
    server.start()
    logging.info("Python language worker serving on %d", port)
    try:
        while True:
            time.sleep(_ONE_DAY_IN_SECONDS)
    except KeyboardInterrupt:
        server.stop(0)

if __name__ == "__main__":
    serve(50051)
