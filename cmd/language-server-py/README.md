# Python Language Worker

This is a gRPC server that executes Python code for the Composer workflow engine.

## Installation

Using UV (recommended):
```bash
uv sync
```

Or using pip:
```bash
pip install -r requirements.txt
```

## Generate Protobuf Files

```bash
# Using UV
uv run python -m grpc_tools.protoc -I../../pkg/api/proto --python_out=. --grpc_python_out=. ../../pkg/api/proto/workflow_executor.proto

# Or using Makefile from project root
make proto-py
```

## Usage

```bash
# Using UV
PORT=50051 uv run python main.py

# Or using pip
PORT=50051 python main.py
```

## Environment Variables

- `PORT` - gRPC server port (default: 50051)

