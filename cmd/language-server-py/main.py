#!/usr/bin/env python3

"""
Python Language Worker

This worker exposes a gRPC service for executing Python code.

Usage:
    PORT=50051 python cmd/language-server-py/main.py

Environment Variables:
    PORT - gRPC server port (default: 50051)
"""

import os
import sys
import multiprocessing

# Add current directory to path for imports
sys.path.insert(0, os.path.dirname(__file__))

from server import serve
from config import DEFAULT_PORT

if __name__ == "__main__":
    # Use "fork" start method for multiprocessing — required on macOS (default
    # changed to "spawn" in Python 3.12+) and fastest on Linux. The sandbox
    # child processes inherit the parent's loaded modules (RestrictedPython etc.)
    # so fork avoids re-importing them on every request.
    multiprocessing.set_start_method("fork", force=True)
    port = int(os.getenv("PORT", DEFAULT_PORT))
    serve(port)
