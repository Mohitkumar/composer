"""
Configuration constants for the Python language server
"""

# Default timeout: 30 seconds
DEFAULT_TIMEOUT_SECONDS = 30

# Maximum code size: 1MB (prevent DoS)
MAX_CODE_SIZE = 1024 * 1024

# Maximum memory per sandbox process: 256MB
MAX_MEMORY_BYTES = 256 * 1024 * 1024

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

