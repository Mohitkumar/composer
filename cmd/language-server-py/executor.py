"""
Code execution logic for Python language server
"""

import json
import threading
import logging
import traceback
import types
from RestrictedPython import compile_restricted
from sandbox import TimeoutError, create_safe_globals
from config import DEFAULT_TIMEOUT_SECONDS, MAX_CODE_SIZE

logger = logging.getLogger(__name__)


def validate_code_size(code):
    """
    Validate code size
    
    Args:
        code: Code to validate
        
    Raises:
        ValueError: If code exceeds maximum size
    """
    code_size = len(code)
    logger.debug(f"Validating code size: {code_size} bytes (max: {MAX_CODE_SIZE})")
    if code_size > MAX_CODE_SIZE:
        error_msg = f"Code size ({code_size} bytes) exceeds maximum allowed size of {MAX_CODE_SIZE} bytes"
        logger.error(error_msg)
        raise ValueError(error_msg)
    logger.debug(f"Code size validation passed")


def parse_input(input_json):
    """
    Validate and parse input JSON
    
    Args:
        input_json: JSON string input
        
    Returns:
        dict: Parsed input object
        
    Raises:
        ValueError: If JSON is invalid
    """
    try:
        logger.debug(f"Parsing input JSON (length: {len(input_json or '{}')})")
        parsed = json.loads(input_json or "{}")
        logger.debug(f"Input JSON parsed successfully, type: {type(parsed)}")
        return parsed
    except json.JSONDecodeError as e:
        error_msg = f"Invalid input_json: {str(e)}"
        logger.error(f"{error_msg} - JSON: {input_json[:200]}")
        raise ValueError(error_msg)


def calculate_timeout(timeout_seconds):
    """
    Calculate timeout in seconds
    
    Args:
        timeout_seconds: Timeout in seconds from request
        
    Returns:
        int: Timeout in seconds (capped at default)
    """
    timeout = timeout_seconds or DEFAULT_TIMEOUT_SECONDS
    return min(timeout, DEFAULT_TIMEOUT_SECONDS)


def execute_with_timeout(code, safe_globals, local_ns, timeout_seconds):
    """
    Execute code with timeout using threading
    
    Args:
        code: Code to execute
        safe_globals: Safe global namespace
        local_ns: Local namespace for execution
        timeout_seconds: Timeout in seconds
        
    Raises:
        TimeoutError: If execution times out
        Exception: If execution fails
    """
    exception = [None]
    
    def target():
        try:
            logger.debug("Compiling code with RestrictedPython")
            # Compile with RestrictedPython
            # compile_restricted returns a code object directly (not a result object)
            try:
                compiled_code = compile_restricted(code, '<inline>', 'exec')
            except SyntaxError as e:
                # RestrictedPython may raise SyntaxError for invalid code
                error_msg = f"RestrictedPython compilation failed: {str(e)}"
                logger.error(f"Compilation failed: {error_msg}")
                exception[0] = SyntaxError(error_msg)
                return
            except Exception as e:
                # Other compilation errors
                error_msg = f"RestrictedPython compilation error: {type(e).__name__}: {str(e)}"
                logger.error(f"Compilation failed: {error_msg}")
                exception[0] = SyntaxError(error_msg)
                return
            
            # Verify we got a code object
            if not isinstance(compiled_code, types.CodeType):
                error_msg = "RestrictedPython compilation did not produce valid bytecode"
                logger.error(f"Compilation failed: {error_msg}")
                exception[0] = SyntaxError(error_msg)
                return
            
            logger.debug("Code compiled successfully, executing bytecode")
            # Execute the bytecode directly (it's already a code object)
            exec(compiled_code, safe_globals, local_ns)
            logger.debug("Code execution completed successfully")
        except Exception as e:
            logger.error(f"Error during code execution: {type(e).__name__}: {str(e)}")
            logger.debug(f"Execution error traceback:\n{traceback.format_exc()}")
            exception[0] = e
    
    logger.debug(f"Starting code execution thread (timeout: {timeout_seconds}s)")
    thread = threading.Thread(target=target)
    thread.daemon = True
    thread.start()
    thread.join(timeout=timeout_seconds)
    
    if thread.is_alive():
        # Thread is still running, timeout occurred
        error_msg = f"Execution timed out after {timeout_seconds} seconds"
        logger.error(error_msg)
        raise TimeoutError(error_msg)
    
    if exception[0]:
        logger.error(f"Code execution raised exception: {type(exception[0]).__name__}: {str(exception[0])}")
        raise exception[0]
    
    logger.debug("Code execution completed without errors")


def execute_handler_with_timeout(handler, input_data, timeout_seconds):
    """
    Execute handler function with timeout
    
    Args:
        handler: Handler function to execute
        input_data: Input data for handler
        timeout_seconds: Timeout in seconds
        
    Returns:
        Result from handler execution
        
    Raises:
        TimeoutError: If execution times out
        Exception: If execution fails
    """
    handler_result = [None]
    handler_exception = [None]
    
    def call_handler():
        try:
            logger.debug(f"Calling handler function with input type: {type(input_data)}")
            handler_result[0] = handler(input_data)
            logger.debug(f"Handler returned successfully, result type: {type(handler_result[0])}")
        except Exception as e:
            logger.error(f"Handler raised exception: {type(e).__name__}: {str(e)}")
            logger.debug(f"Handler error traceback:\n{traceback.format_exc()}")
            handler_exception[0] = e
    
    logger.debug(f"Starting handler execution thread (timeout: {timeout_seconds}s)")
    handler_thread = threading.Thread(target=call_handler)
    handler_thread.daemon = True
    handler_thread.start()
    handler_thread.join(timeout=timeout_seconds)
    
    if handler_thread.is_alive():
        error_msg = f"Handler execution timed out after {timeout_seconds} seconds"
        logger.error(error_msg)
        raise TimeoutError(error_msg)
    
    if handler_exception[0]:
        logger.error(f"Handler execution failed: {type(handler_exception[0]).__name__}: {str(handler_exception[0])}")
        raise handler_exception[0]
    
    logger.debug("Handler execution completed successfully")
    return handler_result[0]


def execute_code(code, input_data, timeout_seconds):
    """
    Execute Python code in a secure sandbox
    
    Args:
        code: User code to execute
        input_data: Input data for the handler
        timeout_seconds: Timeout in seconds
        
    Returns:
        tuple: (handler function, local namespace)
        
    Raises:
        ValueError: If code validation fails
        TimeoutError: If execution times out
        Exception: If execution fails
    """
    logger.debug("Starting code execution")
    
    # Validate code size
    validate_code_size(code)
    
    # Calculate timeout
    timeout = calculate_timeout(timeout_seconds)
    logger.debug(f"Using timeout: {timeout}s (requested: {timeout_seconds}s)")
    
    # Create safe execution environment
    logger.debug("Creating safe execution environment")
    safe_globals = create_safe_globals(input_data)
    local_ns = {}
    
    # Execute with timeout
    logger.debug("Executing code with timeout")
    execute_with_timeout(code, safe_globals, local_ns, timeout)
    
    # Expect handler function
    logger.debug("Checking for handler function in local namespace")
    if "handler" not in local_ns:
        error_msg = "No handler() function defined in code"
        logger.error(error_msg)
        logger.debug(f"Available names in local namespace: {list(local_ns.keys())}")
        raise ValueError(error_msg)
    
    if not callable(local_ns["handler"]):
        error_msg = "handler is defined but is not callable"
        logger.error(f"{error_msg}, type: {type(local_ns['handler'])}")
        raise ValueError(error_msg)
    
    logger.debug("Handler function found and validated")
    return local_ns["handler"], local_ns


def serialize_output(output):
    """
    Serialize output to JSON
    
    Args:
        output: Output to serialize
        
    Returns:
        str: JSON string
        
    Raises:
        ValueError: If serialization fails
    """
    try:
        logger.debug(f"Serializing output to JSON, type: {type(output)}")
        result = json.dumps(output, default=str)
        logger.debug(f"Output serialized successfully, length: {len(result)}")
        return result
    except (TypeError, ValueError) as e:
        error_msg = f"Failed to serialize output: {str(e)}. Output must be JSON-serializable."
        logger.error(error_msg)
        logger.debug(f"Output that failed to serialize: {output}")
        logger.debug(f"Serialization error traceback:\n{traceback.format_exc()}")
        raise ValueError(error_msg)

