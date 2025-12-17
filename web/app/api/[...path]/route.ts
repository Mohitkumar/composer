import { NextRequest, NextResponse } from 'next/server';

const API_BASE_URL = process.env.NEXT_PUBLIC_API_URL || 'http://localhost:8080';

export async function GET(
  request: NextRequest,
  context: { params: { path: string[] } | Promise<{ path: string[] }> }
) {
  const params = await Promise.resolve(context.params);
  return proxyRequest(request, params?.path, 'GET');
}

export async function POST(
  request: NextRequest,
  context: { params: { path: string[] } | Promise<{ path: string[] }> }
) {
  const params = await Promise.resolve(context.params);
  return proxyRequest(request, params?.path, 'POST');
}

export async function PUT(
  request: NextRequest,
  context: { params: { path: string[] } | Promise<{ path: string[] }> }
) {
  const params = await Promise.resolve(context.params);
  return proxyRequest(request, params?.path, 'PUT');
}

export async function DELETE(
  request: NextRequest,
  context: { params: { path: string[] } | Promise<{ path: string[] }> }
) {
  const params = await Promise.resolve(context.params);
  return proxyRequest(request, params?.path, 'DELETE');
}

async function proxyRequest(
  request: NextRequest,
  pathSegments: string[] | undefined,
  method: string
) {
  try {
    // Safety check for pathSegments
    if (!pathSegments || pathSegments.length === 0) {
      // Fallback: extract path from URL
      const url = new URL(request.url);
      const pathMatch = url.pathname.match(/^\/api\/(.+)$/);
      if (pathMatch) {
        pathSegments = pathMatch[1].split('/');
      } else {
        throw new Error('Invalid path segments');
      }
    }

    // When frontend calls /api/api/v1/workflows, the catch-all route receives:
    // pathSegments = ['api', 'v1', 'workflows', ...]
    // We need to reconstruct the full path: api/v1/workflows/...
    const path = pathSegments.join('/');
    const url = new URL(request.url);
    const queryString = url.search;
    
    // Construct the target URL: http://localhost:8080/api/v1/workflows?...
    const targetUrl = `${API_BASE_URL}/${path}${queryString}`;
    
    console.log(`[API Proxy] ${method} ${targetUrl}`, {
      pathSegments,
      path,
      queryString,
      baseURL: API_BASE_URL,
    });

    // Get request body if present
    let body: string | undefined;
    if (method === 'POST' || method === 'PUT' || method === 'DELETE') {
      try {
        body = await request.text();
      } catch (e) {
        // No body
      }
    }

    // Get headers
    const headers: HeadersInit = {
      'Content-Type': 'application/json',
    };

    // Forward authorization header if present
    const authHeader = request.headers.get('authorization');
    if (authHeader) {
      headers['Authorization'] = authHeader;
    }

    // Make the request to the backend
    let response: Response;
    try {
      response = await fetch(targetUrl, {
        method,
        headers,
        body: body || undefined,
      });
    } catch (fetchError: any) {
      console.error('[API Proxy] Fetch error:', fetchError);
      return NextResponse.json(
        {
          error: 'Failed to connect to backend',
          message: fetchError.message || 'Please ensure the backend server is running',
        },
        {
          status: 503,
          headers: {
            'Access-Control-Allow-Origin': '*',
            'Access-Control-Allow-Methods': 'GET, POST, PUT, DELETE, OPTIONS',
            'Access-Control-Allow-Headers': 'Content-Type, Authorization',
            'Content-Type': 'application/json',
          },
        }
      );
    }

    // Get response data
    const contentType = response.headers.get('content-type');
    let responseData;
    
    try {
      if (contentType?.includes('application/json')) {
        const text = await response.text();
        responseData = text ? JSON.parse(text) : {};
      } else {
        responseData = await response.text();
      }
    } catch (parseError) {
      console.error('[API Proxy] Parse error:', parseError);
      responseData = { error: 'Failed to parse response' };
    }

    // Return response with CORS headers
    return NextResponse.json(responseData, {
      status: response.status,
      statusText: response.statusText,
      headers: {
        'Access-Control-Allow-Origin': '*',
        'Access-Control-Allow-Methods': 'GET, POST, PUT, DELETE, OPTIONS',
        'Access-Control-Allow-Headers': 'Content-Type, Authorization',
        'Content-Type': 'application/json',
      },
    });
  } catch (error: any) {
    console.error('[API Proxy] Error:', error);
    return NextResponse.json(
      { 
        error: error.message || 'Proxy request failed',
        details: process.env.NODE_ENV === 'development' ? error.stack : undefined
      },
      { 
        status: 500,
        headers: {
          'Access-Control-Allow-Origin': '*',
          'Access-Control-Allow-Methods': 'GET, POST, PUT, DELETE, OPTIONS',
          'Access-Control-Allow-Headers': 'Content-Type, Authorization',
        },
      }
    );
  }
}

// Handle OPTIONS for CORS preflight
export async function OPTIONS() {
  return new NextResponse(null, {
    status: 200,
    headers: {
      'Access-Control-Allow-Origin': '*',
      'Access-Control-Allow-Methods': 'GET, POST, PUT, DELETE, OPTIONS',
      'Access-Control-Allow-Headers': 'Content-Type, Authorization',
    },
  });
}
