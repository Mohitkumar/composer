"""
API routes for the Composer AI Service.
"""
from fastapi import APIRouter, HTTPException
from pydantic import BaseModel
from typing import List, Optional

from services.error_summarizer import ErrorSummarizer
from services.node_definition_generator import NodeDefinitionGenerator


# Request/Response models
class ErrorSummaryRequest(BaseModel):
    workflow_id: str
    errors: List[str]


class ErrorSummaryResponse(BaseModel):
    summary: str


class NodeDefinitionRequest(BaseModel):
    node_type: str
    runtime: str
    description: Optional[str] = None
    requirements: Optional[List[str]] = None


class NodeDefinitionResponse(BaseModel):
    node_definition: dict
    explanation: str


# Router
router = APIRouter()

# Service instances (lazy initialization)
_error_summarizer = None
_node_generator = None


def get_error_summarizer() -> ErrorSummarizer:
    """Get or create error summarizer instance."""
    global _error_summarizer
    if _error_summarizer is None:
        try:
            _error_summarizer = ErrorSummarizer()
        except ValueError as e:
            raise HTTPException(
                status_code=500,
                detail=f"Error summarizer not initialized. Check GOOGLE_API_KEY environment variable: {str(e)}",
            )
    return _error_summarizer


def get_node_generator() -> NodeDefinitionGenerator:
    """Get or create node generator instance."""
    global _node_generator
    if _node_generator is None:
        try:
            _node_generator = NodeDefinitionGenerator()
        except ValueError as e:
            raise HTTPException(
                status_code=500,
                detail=f"Node generator not initialized. Check GOOGLE_API_KEY environment variable: {str(e)}",
            )
    return _node_generator


@router.get("/health")
async def health_check():
    """Health check endpoint"""
    return {"status": "healthy"}


@router.post("/summarize-errors", response_model=ErrorSummaryResponse)
async def summarize_errors(request: ErrorSummaryRequest):
    """
    Summarize workflow execution errors using AI.
    
    Takes a list of error messages from failed workflow executions
    and generates a concise, actionable summary.
    """
    try:
        error_summarizer = get_error_summarizer()
        summary = await error_summarizer.summarize(
            workflow_id=request.workflow_id,
            errors=request.errors,
        )
        return ErrorSummaryResponse(summary=summary)
    except HTTPException:
        raise
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Failed to summarize errors: {str(e)}")


@router.post("/generate-node-definition", response_model=NodeDefinitionResponse)
async def generate_node_definition(request: NodeDefinitionRequest):
    """
    Generate a node definition based on requirements.
    
    Creates a complete node definition including spec structure,
    required fields, and example values.
    """
    try:
        node_generator = get_node_generator()
        result = await node_generator.generate(
            node_type=request.node_type,
            runtime=request.runtime,
            description=request.description,
            requirements=request.requirements or [],
        )
        return NodeDefinitionResponse(
            node_definition=result["definition"],
            explanation=result["explanation"],
        )
    except HTTPException:
        raise
    except Exception as e:
        raise HTTPException(
            status_code=500, detail=f"Failed to generate node definition: {str(e)}"
        )
