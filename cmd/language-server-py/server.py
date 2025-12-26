"""
gRPC server setup and request handling
"""

import os
import sys
import time
import logging
import traceback
from concurrent import futures
from datetime import datetime
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

# Setup logging with file output
def setup_logging():
    """Setup logging configuration with file and console output"""
    log_dir = os.path.join(os.path.dirname(__file__), '../../logs')
    os.makedirs(log_dir, exist_ok=True)
    log_file = os.path.join(log_dir, 'language-server-py.log')
    
    # Create formatter
    formatter = logging.Formatter(
        '%(asctime)s - %(name)s - %(levelname)s - %(message)s',
        datefmt='%Y-%m-%d %H:%M:%S'
    )
    
    # File handler
    file_handler = logging.FileHandler(log_file, mode='a')
    file_handler.setLevel(logging.DEBUG)
    file_handler.setFormatter(formatter)
    
    # Console handler
    console_handler = logging.StreamHandler(sys.stderr)
    console_handler.setLevel(logging.INFO)
    console_handler.setFormatter(formatter)
    
    # Root logger
    root_logger = logging.getLogger()
    root_logger.setLevel(logging.DEBUG)
    root_logger.addHandler(file_handler)
    root_logger.addHandler(console_handler)
    
    return logging.getLogger(__name__)

logger = setup_logging()


class LanguageExecutorServicer(pb_grpc.LanguageExecutorServicer):
    """Implements the LanguageExecutor gRPC service"""
    
    def Execute(self, request, context):
        start = time.time()
        task_id = request.task_id or "unknown"
        logs = []
        
        try:
            runtime = request.runtime
            logger.info(f"[{task_id}] Starting execution - runtime: {runtime}")
            
            # Check payload type - WhichOneof is called on the message object, not on a field
            payload_type = request.WhichOneof("payload")
            logger.debug(f"[{task_id}] Payload type: {payload_type}")
            
            if payload_type != "inline_code":
                error_msg = f"Only inline_code payload is supported, got: {payload_type}"
                logger.error(f"[{task_id}] {error_msg}")
                return pb.ExecuteResponse(
                    success=False,
                    error=error_msg,
                    duration_ms=int((time.time() - start) * 1000)
                )
            
            # Access inline_code directly from request, not request.payload
            code = request.inline_code.code
            filename = request.inline_code.filename or "handler.py"
            input_json = request.input_json or "{}"
            timeout_seconds = request.timeout_seconds or DEFAULT_TIMEOUT_SECONDS
            
            # Log request details (truncated for security)
            code_preview = code[:200] + "..." if len(code) > 200 else code
            input_preview = input_json[:500] + "..." if len(input_json) > 500 else input_json
            logger.debug(f"[{task_id}] Code preview ({len(code)} chars): {code_preview}")
            logger.debug(f"[{task_id}] Input preview ({len(input_json)} chars): {input_preview}")
            logger.debug(f"[{task_id}] Filename: {filename}, Timeout: {timeout_seconds}s")
            
            # Parse input
            try:
                logger.debug(f"[{task_id}] Parsing input JSON")
                input_data = parse_input(input_json)
                logger.debug(f"[{task_id}] Input parsed successfully, type: {type(input_data)}")
            except ValueError as e:
                error_msg = f"Failed to parse input JSON: {str(e)}"
                logger.error(f"[{task_id}] {error_msg}")
                logger.debug(f"[{task_id}] Input JSON that failed: {input_json[:500]}")
                return pb.ExecuteResponse(
                    success=False,
                    error=error_msg,
                    duration_ms=int((time.time() - start) * 1000)
                )
            
            # Execute code
            try:
                logger.info(f"[{task_id}] Executing code (timeout: {timeout_seconds}s)")
                code_start = time.time()
                handler, local_ns = execute_code(code, input_data, timeout_seconds)
                code_duration = time.time() - code_start
                logger.debug(f"[{task_id}] Code execution completed in {code_duration:.3f}s, handler found: {handler is not None}")
                
                # Call handler with input (with timeout)
                logger.debug(f"[{task_id}] Calling handler function")
                handler_start = time.time()
                output = execute_handler_with_timeout(handler, input_data, timeout_seconds)
                handler_duration = time.time() - handler_start
                logger.debug(f"[{task_id}] Handler execution completed in {handler_duration:.3f}s")
                logger.debug(f"[{task_id}] Handler output type: {type(output)}")
                
                # Serialize output
                try:
                    logger.debug(f"[{task_id}] Serializing output to JSON")
                    output_json = serialize_output(output)
                    output_preview = output_json[:500] + "..." if len(output_json) > 500 else output_json
                    logger.debug(f"[{task_id}] Output serialized ({len(output_json)} chars): {output_preview}")
                except ValueError as e:
                    error_msg = f"Failed to serialize output: {str(e)}"
                    logger.error(f"[{task_id}] {error_msg}")
                    logger.debug(f"[{task_id}] Output that failed to serialize: {output}")
                    return pb.ExecuteResponse(
                        success=False,
                        error=error_msg,
                        duration_ms=int((time.time() - start) * 1000)
                    )
                
                duration = int((time.time() - start) * 1000)
                logger.info(f"[{task_id}] Execution successful - duration: {duration}ms")
                
                return pb.ExecuteResponse(
                    success=True,
                    output_json=output_json,
                    duration_ms=duration,
                    logs=logs
                )
            except TimeoutError as e:
                error_msg = f"Execution timed out: {str(e)}"
                logger.error(f"[{task_id}] {error_msg}")
                logger.debug(f"[{task_id}] Timeout after {timeout_seconds}s")
                return pb.ExecuteResponse(
                    success=False,
                    error=error_msg,
                    duration_ms=int((time.time() - start) * 1000)
                )
            except ImportError as e:
                error_msg = f"Import error: {str(e)}"
                logger.error(f"[{task_id}] {error_msg}")
                logger.debug(f"[{task_id}] Import error traceback:\n{traceback.format_exc()}")
                return pb.ExecuteResponse(
                    success=False,
                    error=error_msg,
                    duration_ms=int((time.time() - start) * 1000)
                )
            except ValueError as e:
                error_msg = f"Validation error: {str(e)}"
                logger.error(f"[{task_id}] {error_msg}")
                logger.debug(f"[{task_id}] Validation error traceback:\n{traceback.format_exc()}")
                return pb.ExecuteResponse(
                    success=False,
                    error=error_msg,
                    duration_ms=int((time.time() - start) * 1000)
                )
            except SyntaxError as e:
                error_msg = f"Syntax error: {str(e)}"
                logger.error(f"[{task_id}] {error_msg}")
                logger.debug(f"[{task_id}] Syntax error details: {e.msg} at line {e.lineno}")
                logger.debug(f"[{task_id}] Syntax error traceback:\n{traceback.format_exc()}")
                return pb.ExecuteResponse(
                    success=False,
                    error=error_msg,
                    duration_ms=int((time.time() - start) * 1000)
                )
            except Exception as e:
                error_msg = f"Execution error: {str(e)}"
                error_type = type(e).__name__
                logger.error(f"[{task_id}] {error_type}: {error_msg}")
                logger.error(f"[{task_id}] Full traceback:\n{traceback.format_exc()}")
                return pb.ExecuteResponse(
                    success=False,
                    error=error_msg,
                    duration_ms=int((time.time() - start) * 1000)
                )
        except Exception as e:
            error_type = type(e).__name__
            logger.error(f"[{task_id}] Unexpected error in Execute: {error_type}: {str(e)}")
            logger.error(f"[{task_id}] Full traceback:\n{traceback.format_exc()}")
            return pb.ExecuteResponse(
                success=False,
                error=f"Unexpected error: {str(e)}",
                duration_ms=int((time.time() - start) * 1000)
            )


def serve(port=50051):
    """Start the gRPC server"""
    logger.info("=" * 60)
    logger.info("Starting Python Language Server")
    logger.info(f"Port: {port}")
    logger.info(f"Default timeout: {DEFAULT_TIMEOUT_SECONDS}s")
    logger.info("=" * 60)
    
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=4))
    pb_grpc.add_LanguageExecutorServicer_to_server(
        LanguageExecutorServicer(),
        server
    )
    
    listen_addr = f"[::]:{port}"
    server.add_insecure_port(listen_addr)
    server.start()
    
    logger.info(f"Python language worker serving on {listen_addr}")
    logger.info("Ready to accept requests")
    
    try:
        while True:
            time.sleep(86400)  # Sleep for a day
    except KeyboardInterrupt:
        logger.info("Received shutdown signal")
        logger.info("Shutting down server...")
        server.stop(0)
        logger.info("Server stopped")

