"""
gRPC server setup and request handling
"""

import os
import sys
import time
import logging
from concurrent import futures
import grpc

# Add proto path
sys.path.insert(0, os.path.dirname(__file__))

# Import generated protobuf code
try:
    import workflow_executor_pb2 as pb
    import workflow_executor_pb2_grpc as pb_grpc
except ImportError:
    print("Error: Protobuf files not found. Please generate them first:")
    print("  make proto-py")
    print("  Or:")
    print("  python -m grpc_tools.protoc -I../../pkg/api/proto --python_out=. --grpc_python_out=. ../../pkg/api/proto/workflow_executor.proto")
    sys.exit(1)

from executor import (
    execute_code,
    parse_input,
    serialize_output,
    execute_handler_with_timeout
)
from sandbox import TimeoutError
from config import DEFAULT_TIMEOUT_SECONDS

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
                    error="Only inline_code payload is supported",
                    duration_ms=int((time.time() - start) * 1000)
                )
            
            code = request.payload.inline_code.code
            filename = request.payload.inline_code.filename or "handler.py"
            input_json = request.input_json or "{}"
            timeout_seconds = request.timeout_seconds or DEFAULT_TIMEOUT_SECONDS
            
            # Parse input
            try:
                input_data = parse_input(input_json)
            except ValueError as e:
                return pb.ExecuteResponse(
                    success=False,
                    error=str(e),
                    duration_ms=int((time.time() - start) * 1000)
                )
            
            # Execute code
            try:
                handler, local_ns = execute_code(code, input_data, timeout_seconds)
                
                # Call handler with input (with timeout)
                output = execute_handler_with_timeout(handler, input_data, timeout_seconds)
                
                # Serialize output
                try:
                    output_json = serialize_output(output)
                except ValueError as e:
                    return pb.ExecuteResponse(
                        success=False,
                        error=str(e),
                        duration_ms=int((time.time() - start) * 1000)
                    )
                
                duration = int((time.time() - start) * 1000)
                
                return pb.ExecuteResponse(
                    success=True,
                    output_json=output_json,
                    duration_ms=duration,
                    logs=logs
                )
            except TimeoutError as e:
                return pb.ExecuteResponse(
                    success=False,
                    error=str(e),
                    duration_ms=int((time.time() - start) * 1000)
                )
            except ImportError as e:
                return pb.ExecuteResponse(
                    success=False,
                    error=f"Import error: {str(e)}",
                    duration_ms=int((time.time() - start) * 1000)
                )
            except ValueError as e:
                return pb.ExecuteResponse(
                    success=False,
                    error=str(e),
                    duration_ms=int((time.time() - start) * 1000)
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
                error=str(e),
                duration_ms=int((time.time() - start) * 1000)
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

