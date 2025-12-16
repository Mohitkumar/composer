"""
Secure sandbox creation and management for Python code execution
"""

import builtins
from config import ALLOWED_IMPORTS, BLOCKED_IMPORTS


class TimeoutError(Exception):
    """Custom timeout exception"""
    pass


class SecureImporter:
    """Custom import handler that enforces whitelist"""
    
    def __init__(self, allowed_imports, blocked_imports):
        self.allowed_imports = allowed_imports
        self.blocked_imports = blocked_imports
        # Get the original __import__ function
        self._original_import = builtins.__import__
    
    def __call__(self, name, globals=None, locals=None, fromlist=(), level=0):
        # Extract base module name
        base_module = name.split('.')[0]
        
        # Check if blocked
        if base_module in self.blocked_imports:
            raise ImportError(f"Import of '{base_module}' is not allowed for security reasons")
        
        # Check if allowed
        if base_module in self.allowed_imports:
            return self._original_import(name, globals, locals, fromlist, level)
        
        # For unknown modules, be conservative and block
        # This prevents importing dangerous modules
        raise ImportError(
            f"Import of '{base_module}' is not in the allowed list. "
            f"Allowed imports: {', '.join(sorted(self.allowed_imports))}"
        )


def create_safe_globals(input_data):
    """
    Create a safe global namespace for code execution
    
    Args:
        input_data: Input data to make available in the sandbox
        
    Returns:
        dict: Safe global namespace
    """
    # Create restricted builtins
    restricted_builtins = {
        # Safe built-ins only
        'abs': abs, 'all': all, 'any': any, 'bool': bool,
        'dict': dict, 'enumerate': enumerate, 'filter': filter,
        'float': float, 'int': int, 'len': len, 'list': list,
        'map': map, 'max': max, 'min': min, 'range': range,
        'reversed': reversed, 'round': round, 'set': set,
        'sorted': sorted, 'str': str, 'sum': sum, 'tuple': tuple,
        'type': type, 'zip': zip,
        # Restricted import
        '__import__': SecureImporter(ALLOWED_IMPORTS, BLOCKED_IMPORTS),
    }
    
    safe_globs = {
        '__builtins__': restricted_builtins,
        # Input data available as 'input'
        'input': input_data
    }
    return safe_globs

