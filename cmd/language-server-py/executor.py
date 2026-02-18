"""
Code execution logic for Python language server.

Uses multiprocessing instead of threading so that timed-out code can be
forcibly killed (threads cannot be killed in CPython).
"""

import json
import multiprocessing
import logging
import resource
import traceback
import types
from RestrictedPython import compile_restricted
from sandbox import TimeoutError, create_safe_globals
from config import DEFAULT_TIMEOUT_SECONDS, MAX_CODE_SIZE, MAX_MEMORY_BYTES

logger = logging.getLogger(__name__)


def validate_code_size(code):
    """Validate code size is within limits."""
    code_size = len(code)
    if code_size > MAX_CODE_SIZE:
        raise ValueError(
            f"Code size ({code_size} bytes) exceeds maximum allowed size of {MAX_CODE_SIZE} bytes"
        )


def parse_input(input_json):
    """Validate and parse input JSON string into a Python object."""
    try:
        return json.loads(input_json or "{}")
    except json.JSONDecodeError as e:
        raise ValueError(f"Invalid input_json: {str(e)}")


def calculate_timeout(timeout_seconds):
    """Calculate timeout in seconds, capped at DEFAULT_TIMEOUT_SECONDS."""
    timeout = timeout_seconds or DEFAULT_TIMEOUT_SECONDS
    if timeout > DEFAULT_TIMEOUT_SECONDS:
        logger.warning(
            f"Requested timeout {timeout}s exceeds max {DEFAULT_TIMEOUT_SECONDS}s, capping"
        )
    return min(timeout, DEFAULT_TIMEOUT_SECONDS)


def _set_resource_limits():
    """Set resource limits for the child process (Linux/macOS only)."""
    try:
        # Limit virtual memory
        resource.setrlimit(resource.RLIMIT_AS, (MAX_MEMORY_BYTES, MAX_MEMORY_BYTES))
    except (ValueError, AttributeError):
        # RLIMIT_AS may not be available on all platforms
        pass
    try:
        # Limit CPU time (hard kill after timeout + buffer)
        cpu_limit = DEFAULT_TIMEOUT_SECONDS + 5
        resource.setrlimit(resource.RLIMIT_CPU, (cpu_limit, cpu_limit))
    except (ValueError, AttributeError):
        pass


def _sandbox_worker(code, input_data, timeout_seconds, result_pipe):
    """
    Worker function that runs in a child process.
    Compiles and executes user code, calls handler(input), sends result back via pipe.
    """
    _set_resource_limits()

    try:
        # Compile with RestrictedPython
        try:
            compiled_code = compile_restricted(code, '<inline>', 'exec')
        except SyntaxError as e:
            result_pipe.send(('error', 'SyntaxError', f"Compilation failed: {e}"))
            return
        except Exception as e:
            result_pipe.send(('error', 'SyntaxError', f"Compilation error: {type(e).__name__}: {e}"))
            return

        if not isinstance(compiled_code, types.CodeType):
            result_pipe.send(('error', 'SyntaxError', "Compilation did not produce valid bytecode"))
            return

        # Create sandbox and execute code to define handler()
        safe_globals = create_safe_globals(input_data)
        local_ns = {}
        exec(compiled_code, safe_globals, local_ns)

        # Validate handler exists
        if "handler" not in local_ns:
            result_pipe.send(('error', 'ValueError', "No handler() function defined in code"))
            return
        if not callable(local_ns["handler"]):
            result_pipe.send(('error', 'ValueError', "handler is defined but is not callable"))
            return

        # Call handler(input)
        output = local_ns["handler"](input_data)

        # Serialize output
        output_json = json.dumps(output, default=str)
        result_pipe.send(('ok', output_json))
    except MemoryError:
        result_pipe.send(('error', 'MemoryError', "Code exceeded memory limit"))
    except Exception as e:
        result_pipe.send(('error', type(e).__name__, str(e)))


def execute_and_call(code, input_data, timeout_seconds):
    """
    Execute user code and call handler(input) in an isolated child process.

    The child process is forcibly killed on timeout, preventing runaway code
    from consuming resources.

    Returns:
        str: JSON-serialized output from handler(input)

    Raises:
        TimeoutError: If execution exceeds timeout
        SyntaxError: If code fails to compile
        ValueError: If handler is missing or output can't be serialized
        Exception: For other execution errors
    """
    validate_code_size(code)
    timeout = calculate_timeout(timeout_seconds)

    parent_conn, child_conn = multiprocessing.Pipe(duplex=False)
    proc = multiprocessing.Process(
        target=_sandbox_worker,
        args=(code, input_data, timeout, child_conn),
        daemon=True,
    )
    proc.start()
    child_conn.close()  # Parent doesn't write to this end

    # Wait for the process to finish within the timeout
    proc.join(timeout=timeout)

    if proc.is_alive():
        # Kill the runaway process
        logger.warning(f"Killing timed-out sandbox process (pid={proc.pid})")
        proc.kill()
        proc.join(timeout=2)
        raise TimeoutError(f"Execution timed out after {timeout} seconds")

    # Process finished — read result from pipe
    if not parent_conn.poll():
        # Process exited without sending a result (e.g. killed by OOM, segfault)
        exit_code = proc.exitcode
        if exit_code == -9:
            raise MemoryError("Process killed (likely exceeded memory limit)")
        raise RuntimeError(f"Sandbox process exited unexpectedly (exit code: {exit_code})")

    msg = parent_conn.recv()
    parent_conn.close()

    if msg[0] == 'ok':
        return msg[1]  # JSON string
    else:
        # msg = ('error', error_type, error_message)
        error_type = msg[1]
        error_message = msg[2]
        if error_type == 'SyntaxError':
            raise SyntaxError(error_message)
        elif error_type == 'ValueError':
            raise ValueError(error_message)
        elif error_type == 'MemoryError':
            raise MemoryError(error_message)
        elif error_type == 'ImportError':
            raise ImportError(error_message)
        else:
            raise RuntimeError(f"{error_type}: {error_message}")


def serialize_output(output):
    """Serialize output to JSON string."""
    try:
        return json.dumps(output, default=str)
    except (TypeError, ValueError) as e:
        raise ValueError(f"Failed to serialize output: {e}. Output must be JSON-serializable.")
