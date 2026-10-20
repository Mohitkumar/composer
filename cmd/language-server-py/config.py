"""
Configuration constants for the Python language server
"""

# Default timeout: 30 seconds
DEFAULT_TIMEOUT_SECONDS = 30

# Maximum code size: 1MB (prevent DoS)
MAX_CODE_SIZE = 1024 * 1024

# Maximum memory per sandbox process: 256MB
MAX_MEMORY_BYTES = 256 * 1024 * 1024

# Maximum size of a handler's JSON output: 10MB
MAX_OUTPUT_BYTES = 10 * 1024 * 1024

# Maximum open file descriptors in a sandbox process
MAX_OPEN_FILES = 64

# gRPC server max worker threads
MAX_WORKERS = 10

# Default server port
DEFAULT_PORT = 50051

# Whitelist of allowed imports (safe libraries)
ALLOWED_IMPORTS = {
    'json', 'math', 'random', 'datetime', 'collections', 'itertools',
    'functools', 'operator', 'string', 're', 'decimal', 'fractions',
    'statistics', 'array', 'bisect', 'heapq', 'copy', 'enum',
    # Data science libraries (as requested)
    'pandas', 'numpy', 'scipy', 'sklearn', 'matplotlib', 'seaborn',
    'pytz', 'dateutil'
}

# Dangerous imports that should be blocked
BLOCKED_IMPORTS = {
    'os', 'sys', 'subprocess', 'shutil', 'socket', 'urllib', 'http',
    'ftplib', 'smtplib', 'telnetlib', 'pickle', 'marshal', 'ctypes',
    'multiprocessing', 'threading', 'asyncio', 'importlib', '__builtin__',
    'builtins', 'eval', 'exec', 'compile', 'open', 'file', 'input', 'raw_input'
}

# Attribute names user code may not read, whatever the object: functions that
# evaluate strings as code or deserialize pickles. (Files and processes are
# blocked at the interpreter level by guard.py, so read_csv etc. stay usable on
# in-memory buffers.)
DENIED_ATTRIBUTES = {
    'eval', 'exec', 'compile', 'query', 'read_pickle', 'to_pickle',
    'system', 'popen', 'globals', 'locals', 'vars', 'getattr', 'setattr', 'delattr',
}
