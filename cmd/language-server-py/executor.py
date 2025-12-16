"""
Code execution logic for Python language server
"""

import json
import threading
from RestrictedPython import compile_restricted
from sandbox import TimeoutError, create_safe_globals
from config import DEFAULT_TIMEOUT_SECONDS, MAX_CODE_SIZE


def validate_code_size(code):
    """
    Validate code size
    
    Args:
        code: Code to validate
        
    Raises:
        ValueError: If code exceeds maximum size
    """
    if len(code) > MAX_CODE_SIZE:
        raise ValueError(f"Code size exceeds maximum allowed size of {MAX_CODE_SIZE} bytes")


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
        return json.loads(input_json or "{}")
    except json.JSONDecodeError as e:
        raise ValueError(f"Invalid input_json: {str(e)}")


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
            # Compile with RestrictedPython
            byte_code = compile_restricted(code, '<inline>', 'exec')
            if byte_code.errors:
                exception[0] = SyntaxError(
                    f"RestrictedPython compilation errors: {', '.join(byte_code.errors)}"
                )
                return
            
            # Execute the bytecode
            exec(byte_code.code, safe_globals, local_ns)
        except Exception as e:
            exception[0] = e
    
    thread = threading.Thread(target=target)
    thread.daemon = True
    thread.start()
    thread.join(timeout=timeout_seconds)
    
    if thread.is_alive():
        # Thread is still running, timeout occurred
        raise TimeoutError(f"Execution timed out after {timeout_seconds} seconds")
    
    if exception[0]:
        raise exception[0]


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
            handler_result[0] = handler(input_data)
        except Exception as e:
            handler_exception[0] = e
    
    handler_thread = threading.Thread(target=call_handler)
    handler_thread.daemon = True
    handler_thread.start()
    handler_thread.join(timeout=timeout_seconds)
    
    if handler_thread.is_alive():
        raise TimeoutError(f"Handler execution timed out after {timeout_seconds} seconds")
    
    if handler_exception[0]:
        raise handler_exception[0]
    
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
    # Validate code size
    validate_code_size(code)
    
    # Calculate timeout
    timeout = calculate_timeout(timeout_seconds)
    
    # Create safe execution environment
    safe_globals = create_safe_globals(input_data)
    local_ns = {}
    
    # Execute with timeout
    execute_with_timeout(code, safe_globals, local_ns, timeout)
    
    # Expect handler function
    if "handler" not in local_ns or not callable(local_ns["handler"]):
        raise ValueError("No handler() function defined in code")
    
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
        return json.dumps(output, default=str)
    except (TypeError, ValueError) as e:
        raise ValueError(
            f"Failed to serialize output: {str(e)}. Output must be JSON-serializable."
        )

