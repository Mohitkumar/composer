"""
gRPC server setup and request handling
"""

import os
import sys
import time
import logging
import traceback
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
    sys.exit(1)

from executor import execute_and_call, parse_input
from sandbox import TimeoutError
from config import DEFAULT_TIMEOUT_SECONDS, MAX_WORKERS

# Setup logging with file output
def setup_logging():
    """Setup logging configuration with file and console output"""
    log_dir = os.path.join(os.path.dirname(__file__), '../../logs')
    os.makedirs(log_dir, exist_ok=True)
    log_file = os.path.join(log_dir, 'language-server-py.log')

    formatter = logging.Formatter(
        '%(asctime)s - %(name)s - %(levelname)s - %(message)s',
        datefmt='%Y-%m-%d %H:%M:%S'
    )

    file_handler = logging.FileHandler(log_file, mode='a')
    file_handler.setLevel(logging.DEBUG)
    file_handler.setFormatter(formatter)

    console_handler = logging.StreamHandler(sys.stderr)
    console_handler.setLevel(logging.INFO)
    console_handler.setFormatter(formatter)

    root_logger = logging.getLogger()
    root_logger.setLevel(logging.INFO)
    root_logger.addHandler(file_handler)
    root_logger.addHandler(console_handler)

    return logging.getLogger(__name__)

logger = setup_logging()


class LanguageExecutorServicer(pb_grpc.LanguageExecutorServicer):
    """Implements the LanguageExecutor gRPC service"""

    def Execute(self, request, context):
        start = time.time()
        task_id = request.task_id or "unknown"

        try:
            runtime = request.runtime
            logger.info(f"[{task_id}] Starting execution - runtime: {runtime}")

            # Check payload type
            payload_type = request.WhichOneof("payload")
            if payload_type != "inline_code":
                error_msg = f"Only inline_code payload is supported, got: {payload_type}"
                logger.error(f"[{task_id}] {error_msg}")
                return pb.ExecuteResponse(
                    success=False,
                    error=error_msg,
                    duration_ms=int((time.time() - start) * 1000)
                )

            code = request.inline_code.code
            filename = request.inline_code.filename or "handler.py"
            input_json = request.input_json or "{}"
            timeout_seconds = request.timeout_seconds or DEFAULT_TIMEOUT_SECONDS

            # Log metadata only (not content) to avoid leaking sensitive data
            logger.info(
                f"[{task_id}] code_size={len(code)} input_size={len(input_json)} "
                f"filename={filename} timeout={timeout_seconds}s"
            )

            # Parse input
            try:
                input_data = parse_input(input_json)
            except ValueError as e:
                logger.error(f"[{task_id}] Input parse error: {e}")
                return pb.ExecuteResponse(
                    success=False,
                    error=f"Failed to parse input JSON: {e}",
                    duration_ms=int((time.time() - start) * 1000)
                )

            # Execute code + call handler in a single sandboxed child process.
            # The entire timeout budget goes to this one call (no double-timeout).
            try:
                output_json = execute_and_call(code, input_data, timeout_seconds)
                duration = int((time.time() - start) * 1000)
                logger.info(f"[{task_id}] Execution successful - duration: {duration}ms")

                return pb.ExecuteResponse(
                    success=True,
                    output_json=output_json,
                    duration_ms=duration,
                )
            except TimeoutError as e:
                logger.error(f"[{task_id}] Timeout: {e}")
                return pb.ExecuteResponse(
                    success=False,
                    error=f"Execution timed out: {e}",
                    duration_ms=int((time.time() - start) * 1000)
                )
            except (SyntaxError, ValueError, ImportError, MemoryError) as e:
                logger.error(f"[{task_id}] {type(e).__name__}: {e}")
                return pb.ExecuteResponse(
                    success=False,
                    error=f"{type(e).__name__}: {e}",
                    duration_ms=int((time.time() - start) * 1000)
                )
            except Exception as e:
                logger.error(f"[{task_id}] {type(e).__name__}: {e}")
                logger.debug(f"[{task_id}] Traceback:\n{traceback.format_exc()}")
                return pb.ExecuteResponse(
                    success=False,
                    error=f"Execution error: {e}",
                    duration_ms=int((time.time() - start) * 1000)
                )
        except Exception as e:
            logger.error(f"[{task_id}] Unexpected error: {type(e).__name__}: {e}")
            logger.debug(f"[{task_id}] Traceback:\n{traceback.format_exc()}")
            return pb.ExecuteResponse(
                success=False,
                error=f"Unexpected error: {e}",
                duration_ms=int((time.time() - start) * 1000)
            )


def serve(port=50051):
    """Start the gRPC server"""
    max_workers = int(os.getenv("MAX_WORKERS", MAX_WORKERS))

    logger.info("=" * 60)
    logger.info("Starting Python Language Server")
    logger.info(f"Port: {port}")
    logger.info(f"Max workers: {max_workers}")
    logger.info(f"Default timeout: {DEFAULT_TIMEOUT_SECONDS}s")
    logger.info("=" * 60)

    server = grpc.server(futures.ThreadPoolExecutor(max_workers=max_workers))
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
            time.sleep(86400)
    except KeyboardInterrupt:
        logger.info("Received shutdown signal")
        # Grace period: allow in-flight RPCs up to 5 seconds to complete
        server.stop(grace=5)
        logger.info("Server stopped")
