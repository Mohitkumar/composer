# Composer AI Service

AI service for workflow error summarization and node definition generation using Google Gemini 2.5 directly via LangChain with structured output.

## Setup

1. Install dependencies using uv:
```bash
cd ai
uv sync
```

2. Create a `.env` file from `.env.example`:
```bash
cp .env.example .env
```

3. Set your Google API key in `.env`:
```
GOOGLE_API_KEY=your_google_api_key_here
```

You can get a Google API key from [Google AI Studio](https://makersuite.google.com/app/apikey).

## Running the Service

```bash
uv run python main.py
```

Or using uvicorn directly:
```bash
uv run uvicorn main:app --host 0.0.0.0 --port 8000 --reload
```

## API Endpoints

### POST /summarize-errors

Summarize workflow execution errors.

**Request:**
```json
{
  "workflow_id": "my_workflow",
  "errors": [
    "Error: CEL resolution failed for field 'url'",
    "Error: Task instance creation failed"
  ]
}
```

**Response:**
```json
{
  "summary": "The workflow is failing due to CEL expression resolution issues..."
}
```

### POST /generate-node-definition

Generate a node definition based on requirements.

**Request:**
```json
{
  "node_type": "TASK",
  "runtime": "javascript",
  "description": "Process order data and calculate totals",
  "requirements": [
    "Access input.order",
    "Use config.tax_rate",
    "Return calculated total"
  ]
}
```

**Response:**
```json
{
  "node_definition": {
    "node_id": "process_order",
    "name": "Process Order",
    "type": "TASK",
    "runtime": "javascript",
    "spec": {
      "code": "function handler(input) { ... }"
    }
  },
  "explanation": "Generated TASK node for javascript runtime: Process order data and calculate totals"
}
```

### GET /health

Health check endpoint.

## Configuration

- `GOOGLE_API_KEY`: Your Google API key (get from [Google AI Studio](https://makersuite.google.com/app/apikey))
- `HOST`: Server host (default: 0.0.0.0)
- `PORT`: Server port (default: 8000)

## Model

This service uses Google Gemini 2.0 Flash Experimental model (`gemini-2.0-flash-exp`) for:
- Error summarization with focused, actionable insights
- Node definition generation with creative, context-aware suggestions
