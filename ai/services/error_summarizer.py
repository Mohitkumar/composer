"""
Error summarization service using LangChain with Google Gemini 2.5.
Uses structured output for reliable responses.
"""
import os
from typing import List
from langchain_google_genai import ChatGoogleGenerativeAI
from langchain_core.prompts import ChatPromptTemplate
from pydantic import BaseModel, Field

# Example workflow structure for context
WORKFLOW_EXAMPLE = """
Example Workflow Structure:
```yaml
nodes:
  - node_id: get_job
    name: Get Job Details
    type: TASK
    runtime: http
    spec:
      url: https://api.example.com/job/{{input.job_id}}
      method: GET
      headers:
        Authorization: Bearer {{config.api_token}}
  
  - node_id: process_data
    name: Process Data
    type: TASK
    runtime: javascript
    spec:
      code: |
        function handler(input) {
          const data = input.job || {};
          return { processed: data };
        }
      timeout: 60

edges:
  - from: get_job
    to: process_data
```

Common Node Types:
- TASK: Executes a single operation
- SWITCH: Conditional branching
- PARALLEL: Execute multiple nodes in parallel
- WAIT: Wait for external event

Common Runtimes:
- http: HTTP requests
- javascript: JavaScript code execution
- python: Python code execution
- sql: SQL queries
"""

ERROR_SUMMARY_PROMPT = """You are an expert workflow debugging assistant. Your task is to analyze error messages from workflow executions and provide a concise, actionable summary.

Workflow ID: {workflow_id}

Workflow Structure Context:
{workflow_example}

Error Messages from Failed Executions:
{errors}

Please provide:
1. A brief summary of the main issue(s) (2-3 sentences)
2. Common patterns or root causes identified
3. Suggested fixes or areas to investigate

Format your response as a clear, concise summary that helps developers quickly understand and fix the issues. Focus on actionable insights rather than just listing errors.

Summary:"""


class ErrorSummaryOutput(BaseModel):
    """Structured output for error summary."""
    summary: str = Field(description="A concise, actionable summary of the workflow errors")


class ErrorSummarizer:
    """Service for summarizing workflow execution errors using AI."""

    def __init__(self):
        """Initialize the error summarizer with LangChain and Google Gemini 2.5."""
        api_key = os.getenv("GOOGLE_API_KEY")
        if not api_key:
            raise ValueError("GOOGLE_API_KEY environment variable is required")

        # Use Google Gemini 2.5 Flash directly
        self.llm = ChatGoogleGenerativeAI(
            model="gemini-2.0-flash-exp",
            google_api_key=api_key,
            temperature=0.3,  # Lower temperature for more focused summaries
        )

        # Create prompt template
        self.prompt = ChatPromptTemplate.from_template(ERROR_SUMMARY_PROMPT)
        
        # Create structured output chain
        self.structured_llm = self.llm.with_structured_output(
            ErrorSummaryOutput, method="json_schema"
        )
        self.chain = self.prompt | self.structured_llm

    async def summarize(self, workflow_id: str, errors: List[str]) -> str:
        """
        Summarize a list of error messages.

        Args:
            workflow_id: The workflow identifier
            errors: List of error messages from failed executions

        Returns:
            A concise summary of the errors
        """
        if not errors:
            return "No errors provided for summarization."

        # Format errors for the prompt
        errors_text = "\n".join([f"- {error}" for error in errors])

        try:
            # Invoke the chain with structured output
            result = await self.chain.ainvoke(
                {
                    "workflow_id": workflow_id,
                    "workflow_example": WORKFLOW_EXAMPLE,
                    "errors": errors_text,
                }
            )
            
            # Extract summary from structured response
            return result.summary.strip()
        except Exception as e:
            # Fallback: return a simple summary if AI call fails
            error_count = len(errors)
            unique_errors = len(set(errors))
            return f"Analysis of {error_count} error(s) ({unique_errors} unique). Failed to generate AI summary: {str(e)}. Review the errors manually."
