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

# Add current directory to path for imports
sys.path.insert(0, os.path.dirname(__file__))

from server import serve
from config import DEFAULT_PORT

if __name__ == "__main__":
    port = int(os.getenv("PORT", DEFAULT_PORT))
    serve(port)
