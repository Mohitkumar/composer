#!/usr/bin/env python3

"""
Python Language Worker

This worker exposes a gRPC service for executing Python code.

Usage:
    PORT=50051 python cmd/worker-py/main.py

Environment Variables:
    PORT - gRPC server port (default: 50051)
"""

import os
import sys
import time
import json
import logging
from concurrent import futures
import grpc

# Add proto path
sys.path.insert(0, os.path.dirname(__file__))

# Import generated protobuf code
# Note: These need to be generated first using:
#   python -m grpc_tools.protoc -I../../pkg/api/proto --python_out=. --grpc_python_out=. ../../pkg/api/proto/workflow_executor.proto
#   Or: make proto-py
try:
    import workflow_executor_pb2 as pb
    import workflow_executor_pb2_grpc as pb_grpc
except ImportError:
    print("Error: Protobuf files not found. Please generate them first:")
    print("  make proto-py")
    print("  Or:")
    print("  python -m grpc_tools.protoc -I../../pkg/api/proto --python_out=. --grpc_python_out=. ../../pkg/api/proto/workflow_executor.proto")
    sys.exit(1)

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)


class LanguageExecutorServicer(pb_grpc.LanguageExecutorServicer):
    """Implements the LanguageExecutor gRPC service"""
    
    def Execute(self, request, context):
        start = time.time()
        logs = []
        
        try:
            runtime = request.runtime
            logger.info(f"Executing {runtime} task: {request.task_id}")
            
            # Check payload type
            if request.payload.WhichOneof("payload") != "inline_code":
                return pb.ExecuteResponse(
                    success=False,
                    error="Only inline_code payload is supported"
                )
            
            code = request.payload.inline_code.code
            filename = request.payload.inline_code.filename or "handler.py"
            input_json = request.input_json or "{}"
            
            # Parse input
            try:
                input_data = json.loads(input_json)
            except json.JSONDecodeError as e:
                return pb.ExecuteResponse(
                    success=False,
                    error=f"Invalid input_json: {str(e)}"
                )
            
            # WARNING: This executes arbitrary code - use with caution in production
            # In production, consider using a sandboxed environment
            local_ns = {}
            try:
                # Execute the code in a local namespace
                exec(code, {}, local_ns)
                
                # Expect handler function
                if "handler" not in local_ns or not callable(local_ns["handler"]):
                    return pb.ExecuteResponse(
                        success=False,
                        error="No handler() function defined in code"
                    )
                
                # Call handler with input
                output = local_ns["handler"](input_data)
                output_json = json.dumps(output)
                duration = int((time.time() - start) * 1000)
                
                return pb.ExecuteResponse(
                    success=True,
                    output_json=output_json,
                    duration_ms=duration,
                    logs=logs
                )
            except Exception as e:
                return pb.ExecuteResponse(
                    success=False,
                    error=str(e),
                    duration_ms=int((time.time() - start) * 1000)
                )
        except Exception as e:
            logger.error(f"Execute error: {e}")
            return pb.ExecuteResponse(
                success=False,
                error=str(e)
            )


def serve(port=50051):
    """Start the gRPC server"""
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=4))
    pb_grpc.add_LanguageExecutorServicer_to_server(
        LanguageExecutorServicer(),
        server
    )
    
    listen_addr = f"[::]:{port}"
    server.add_insecure_port(listen_addr)
    server.start()
    
    logger.info(f"Python language worker serving on {listen_addr}")
    
    try:
        while True:
            time.sleep(86400)  # Sleep for a day
    except KeyboardInterrupt:
        logger.info("Shutting down...")
        server.stop(0)


if __name__ == "__main__":
    port = int(os.getenv("PORT", "50051"))
    serve(port)

