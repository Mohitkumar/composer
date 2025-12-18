"""
Node definition generation service using LangChain Agent with Google Gemini 2.5.
Uses the create_agent interface with structured output.
"""
import os
from typing import List, Optional, Dict, Any
from langchain_google_genai import ChatGoogleGenerativeAI
from langchain.agents import create_agent
from langchain.agents.structured_output import ProviderStrategy
from pydantic import BaseModel, Field

# Example node definitions for context
NODE_EXAMPLES = """
Example Node Definitions:

1. HTTP Node:
```json
{{
  "node_id": "http_request",
  "name": "HTTP Request",
  "type": "TASK",
  "runtime": "http",
  "spec": {{
    "url": "https://api.example.com/endpoint",
    "method": "GET",
    "headers": {{
      "Authorization": "Bearer {{config.api_token}}",
      "Content-Type": "application/json"
    }},
    "body": "{{input.data}}",
    "timeout_seconds": 30
  }}
}}
```

2. JavaScript Node:
```json
{{
  "node_id": "process_data",
  "name": "Process Data",
  "type": "TASK",
  "runtime": "javascript",
  "spec": {{
    "code": "function handler(input) {{\\n  // Process input\\n  return {{ result: input.data }};\\n}}",
    "timeout": 60,
    "input": "{{{{\"data\": input.data, \"config\": config}}}}"
  }}
}}
```

3. Python Node:
```json
{{
  "node_id": "analyze_data",
  "name": "Analyze Data",
  "type": "TASK",
  "runtime": "python",
  "spec": {{
    "code": "def handler(input):\\n    # Process input\\n    return {{'result': input.get('data')}}",
    "timeout": 60,
    "requirements": ["pandas", "numpy"]
  }}
}}
```

4. SQL Node:
```json
{{
  "node_id": "query_database",
  "name": "Query Database",
  "type": "TASK",
  "runtime": "sql",
  "spec": {{
    "query": "SELECT * FROM users WHERE id = {{input.user_id}}",
    "database": "{{config.database_name}}",
    "timeout": 30
  }}
}}
```

Node Structure Requirements:
- node_id: Unique identifier (snake_case)
- name: Human-readable name
- type: One of TASK, SWITCH, PARALLEL, WAIT
- runtime: One of http, javascript, python, sql, go
- spec: Runtime-specific configuration object

Template Variables:
- {{input.field_name}}: Access input data
- {{config.config_name}}: Access configuration
- {{tasks.node_id.output}}: Access previous node output
- {{tasks.node_id.output.field}}: Access nested field from previous node
"""

NODE_DEFINITION_PROMPT = """You are an expert workflow node definition generator. Your task is to create a complete, valid node definition based on the requirements provided.

Node Type: {node_type}
Runtime: {runtime}
Description: {description}
Requirements: {requirements}

Example Node Definitions:
{node_examples}

Please generate a complete node definition JSON that:
1. Follows the exact structure shown in the examples
2. Includes all required fields (node_id, name, type, runtime, spec)
3. Uses appropriate spec fields for the given runtime
4. Includes realistic example values
5. Uses template variables ({{input.*}}, {{config.*}}, {{tasks.*}}) where appropriate
6. Has a valid, descriptive node_id in snake_case
7. Has a clear, descriptive name

Return ONLY a valid JSON object with the node definition. Do not include any explanation or markdown formatting, just the raw JSON.

Node Definition:"""


class NodeSpec(BaseModel):
    """Node spec structure - varies by runtime."""
    pass  # Dynamic structure based on runtime


class NodeDefinitionOutput(BaseModel):
    """Structured output for node definition."""
    node_id: str = Field(description="Unique identifier in snake_case")
    name: str = Field(description="Human-readable name")
    type: str = Field(description="Node type: TASK, SWITCH, PARALLEL, or WAIT")
    runtime: str = Field(description="Runtime: http, javascript, python, sql, or go")
    spec: Dict[str, Any] = Field(description="Runtime-specific configuration object")


class NodeDefinitionGenerator:
    """Service for generating node definitions using AI Agent."""

    def __init__(self):
        """Initialize the node generator with LangChain Agent and Google Gemini 2.5."""
        api_key = os.getenv("GOOGLE_API_KEY")
        if not api_key:
            raise ValueError("GOOGLE_API_KEY environment variable is required")

        # Use Google Gemini 2.5 Flash directly
        model = ChatGoogleGenerativeAI(
            model="gemini-2.0-flash-exp",
            google_api_key=api_key,
            temperature=0.7,  # Higher temperature for more creative generation
        )

        # Create agent with structured output using ProviderStrategy
        # Gemini supports native structured output via JSON Schema
        self.agent = create_agent(
            model=model,
            tools=[],  # No tools needed for node generation
            system_prompt="You are an expert workflow node definition generator. Create complete, valid node definitions based on requirements provided by the user.",
            response_format=ProviderStrategy(NodeDefinitionOutput),
        )

    async def generate(
        self,
        node_type: str,
        runtime: str,
        description: Optional[str] = None,
        requirements: Optional[List[str]] = None,
    ) -> dict:
        """
        Generate a node definition based on requirements.

        Args:
            node_type: Type of node (TASK, SWITCH, PARALLEL, WAIT)
            runtime: Runtime to use (http, javascript, python, sql, go)
            description: Optional description of what the node should do
            requirements: Optional list of specific requirements

        Returns:
            Dictionary with 'definition' (node JSON) and 'explanation' (text)
        """
        requirements_text = "\n".join([f"- {req}" for req in (requirements or [])])
        if not requirements_text:
            requirements_text = "No specific requirements provided."

        description_text = description or "No description provided."

        # Build the user message with all context
        user_message = f"""Node Type: {node_type}
Runtime: {runtime}
Description: {description_text}
Requirements: {requirements_text}

Example Node Definitions:
{NODE_EXAMPLES}

Please generate a complete node definition JSON that:
1. Follows the exact structure shown in the examples
2. Includes all required fields (node_id, name, type, runtime, spec)
3. Uses appropriate spec fields for the given runtime
4. Includes realistic example values
5. Uses template variables ({{{{input.*}}}}, {{{{config.*}}}}, {{{{tasks.*}}}}) where appropriate
6. Has a valid, descriptive node_id in snake_case
7. Has a clear, descriptive name

Return ONLY a valid JSON object with the node definition. Do not include any explanation or markdown formatting, just the raw JSON."""

        try:
            # Use agent with structured output
            result = await self.agent.ainvoke(
                {
                    "messages": [{"role": "user", "content": user_message}]
                }
            )
            
            # Extract structured response
            if "structured_response" in result:
                definition = result["structured_response"].model_dump()
            else:
                # Fallback if structured_response not available
                if hasattr(result, "model_dump"):
                    definition = result.model_dump()
                else:
                    definition = dict(result.get("output", {}))

            # Generate explanation
            explanation = f"Generated {node_type} node for {runtime} runtime"
            if description:
                explanation += f": {description}"

            return {
                "definition": definition,
                "explanation": explanation,
            }
        except Exception as e:
            # Fallback: return a basic template
            fallback_definition = {
                "node_id": f"{runtime}_{node_type.lower()}",
                "name": f"{node_type} Node ({runtime})",
                "type": node_type,
                "runtime": runtime,
                "spec": {},
            }

            return {
                "definition": fallback_definition,
                "explanation": f"Generated basic template. AI generation failed: {str(e)}",
            }
