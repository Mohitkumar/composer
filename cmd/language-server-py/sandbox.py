"""
Secure sandbox creation and management for Python code execution
"""

import builtins
import logging
import types
from RestrictedPython.Guards import (
    safer_getattr,
    guarded_setattr,
    guarded_delattr,
    guarded_iter_unpack_sequence,
    guarded_unpack_sequence,
    full_write_guard,
)
from config import ALLOWED_IMPORTS, BLOCKED_IMPORTS, DENIED_ATTRIBUTES

logger = logging.getLogger(__name__)


class TimeoutError(Exception):
    """Custom timeout exception"""
    pass


def _module_allowed(module_name):
    """A module object may be handed to user code only if its package is allowed."""
    root = module_name.split('.')[0]
    return root in ALLOWED_IMPORTS and root not in BLOCKED_IMPORTS


def _check_value(value, name):
    """Refuse to hand out modules that are not allowed.

    Allowed modules expose the modules they import as plain attributes
    (json.codecs, codecs.sys, ...), which would otherwise be a direct path
    from an allowed import to os and sys.
    """
    if isinstance(value, types.ModuleType) and not _module_allowed(value.__name__):
        raise AttributeError(f"access to module '{value.__name__}' (via '{name}') is not allowed")
    return value


def guarded_getattr(obj, name, default=None):
    """_getattr_ for compiled code: RestrictedPython's rules plus module and name denylists."""
    if name in DENIED_ATTRIBUTES:
        raise AttributeError(f"'{name}' is not allowed in the sandbox")
    return _check_value(safer_getattr(obj, name, default), name)


def guarded_hasattr(obj, name):
    try:
        guarded_getattr(obj, name)
        return True
    except Exception:
        return False


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
        
        logger.debug(f"Import attempt: {name} (base: {base_module})")
        
        # Check if blocked
        if base_module in self.blocked_imports:
            error_msg = f"Import of '{base_module}' is not allowed for security reasons"
            logger.warning(f"Blocked import attempt: {name} - {error_msg}")
            raise ImportError(error_msg)
        
        # Check if allowed
        if base_module in self.allowed_imports:
            logger.debug(f"Allowed import: {name}")
            try:
                result = self._original_import(name, globals, locals, fromlist, level)
                # `from pkg import x` reads attributes without _getattr_.
                for item in fromlist or ():
                    if item != '*' and hasattr(result, item):
                        try:
                            _check_value(getattr(result, item), item)
                        except AttributeError as e:
                            raise ImportError(str(e))
                logger.debug(f"Import successful: {name}")
                return result
            except ImportError as e:
                logger.error(f"Import failed for allowed module {name}: {str(e)}")
                raise
            except Exception as e:
                logger.error(f"Unexpected error importing {name}: {type(e).__name__}: {str(e)}")
                raise
        
        # For unknown modules, be conservative and block
        # This prevents importing dangerous modules
        error_msg = (
            f"Import of '{base_module}' is not in the allowed list. "
            f"Allowed imports: {', '.join(sorted(self.allowed_imports))}"
        )
        logger.warning(f"Blocked import attempt: {name} - {error_msg}")
        raise ImportError(error_msg)


def create_safe_globals(input_data):
    """
    Create a safe global namespace for code execution
    
    Args:
        input_data: Input data to make available in the sandbox
        
    Returns:
        dict: Safe global namespace
    """
    logger.debug("Creating safe execution environment")
    logger.debug(f"Input data type: {type(input_data)}")
    logger.debug(f"Allowed imports: {len(ALLOWED_IMPORTS)} modules")
    logger.debug(f"Blocked imports: {len(BLOCKED_IMPORTS)} modules")
    
    # Create restricted builtins
    # SECURITY: Do NOT expose raw getattr/setattr/delattr — RestrictedPython
    # provides guarded versions (_getattr_, _setattr_, _delattr_) that prevent
    # access to dunder attributes and sandbox escape via MRO walking.
    # SECURITY: Do NOT expose type (3-arg form creates classes), vars (exposes
    # __dict__), or dir (enables introspection of restricted objects).

    def _safe_type_check(obj, bases=None, dict_=None):
        """Single-argument type() for type checking only."""
        if bases is not None or dict_ is not None:
            raise TypeError("dynamic class creation is not allowed")
        return type(obj)

    restricted_builtins = {
        # Type checking (safe single-arg wrapper only)
        'isinstance': isinstance,
        'issubclass': issubclass,
        'type': _safe_type_check,
        'callable': callable,
        'hasattr': guarded_hasattr,
        # Basic types
        'bool': bool,
        'int': int,
        'float': float,
        'str': str,
        'dict': dict,
        'list': list,
        'tuple': tuple,
        'set': set,
        'frozenset': frozenset,
        # Math and numeric operations
        'abs': abs,
        'round': round,
        'min': min,
        'max': max,
        'sum': sum,
        'pow': pow,
        'divmod': divmod,
        # Iteration and sequence operations
        'len': len,
        'range': range,
        'enumerate': enumerate,
        'zip': zip,
        'reversed': reversed,
        'sorted': sorted,
        'all': all,
        'any': any,
        'filter': filter,
        'map': map,
        'iter': iter,
        'next': next,
        # Restricted import
        '__import__': SecureImporter(ALLOWED_IMPORTS, BLOCKED_IMPORTS),
    }
    
    # RestrictedPython requires guard functions to be available in globals
    # These are injected by RestrictedPython during compilation
    # The function names must match what RestrictedPython expects in the compiled bytecode
    
    # Helper function for _getitem_ - uses safer_getattr for item access
    def guarded_getitem(obj, key):
        """Guard function for item access (obj[key])"""
        if hasattr(obj, '__getitem__'):
            return _check_value(obj[key], repr(key))  # KeyError/IndexError/TypeError propagate
        return guarded_getattr(obj, key)
    
    # Helper function for _getiter_ - safe iteration
    def guarded_getiter(iterable):
        """Guard function for iteration (for x in iterable)"""
        return iter(iterable)
    
    safe_globs = {
        '__builtins__': restricted_builtins,
        # Input data available as 'input'
        'input': input_data,
        # RestrictedPython guard functions
        # These are required for RestrictedPython to work correctly
        # The names must match what RestrictedPython injects into the compiled code
        '_getattr_': guarded_getattr,  # For attribute access (obj.attr)
        '_getitem_': guarded_getitem,  # For item access (obj[key])
        '_getiter_': guarded_getiter,  # For iteration (for x in iterable)
        '_setattr_': guarded_setattr,  # For attribute assignment (obj.attr = value)
        '_delattr_': guarded_delattr,  # For attribute deletion (del obj.attr)
        '_iter_unpack_': guarded_iter_unpack_sequence,  # For iterable unpacking (a, b = iterable)
        '_unpack_sequence_': guarded_unpack_sequence,  # For sequence unpacking
        '_write_': full_write_guard,  # For write protection
    }
    
    logger.debug("Safe execution environment created successfully")
    return safe_globs

