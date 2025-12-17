-- Insert sample node templates

-- HTTP Node Templates
INSERT INTO node_templates (id, name, description, runtime, node_type, spec_json, category, tags) VALUES
('http-get', 'HTTP GET Request', 'Basic HTTP GET request to fetch data from an API endpoint', 'http', 'TASK', 
 '{"url": "https://api.example.com/data", "method": "GET", "headers": {}, "timeout_seconds": 30}'::jsonb,
 'http', ARRAY['http', 'api', 'get', 'fetch']),

('http-post', 'HTTP POST Request', 'HTTP POST request to send data to an API endpoint', 'http', 'TASK',
 '{"url": "https://api.example.com/data", "method": "POST", "headers": {"Content-Type": "application/json"}, "body": "{}", "timeout_seconds": 30}'::jsonb,
 'http', ARRAY['http', 'api', 'post', 'send']),

('http-put', 'HTTP PUT Request', 'HTTP PUT request to update data via API', 'http', 'TASK',
 '{"url": "https://api.example.com/data/{{input.id}}", "method": "PUT", "headers": {"Content-Type": "application/json"}, "body": "{{input.data}}", "timeout_seconds": 30}'::jsonb,
 'http', ARRAY['http', 'api', 'put', 'update']),

-- JavaScript Node Templates
('js-process', 'JavaScript Processor', 'Process input data using JavaScript', 'javascript', 'TASK',
 '{"code": "function handler(input) {\n  // Process the input data\n  const result = {\n    processed: true,\n    data: input,\n    timestamp: new Date().toISOString()\n  };\n  return result;\n}", "timeout": 60}'::jsonb,
 'code', ARRAY['javascript', 'js', 'process', 'transform']),

('js-transform', 'JavaScript Data Transform', 'Transform and reshape data using JavaScript', 'javascript', 'TASK',
 '{"code": "function handler(input) {\n  // Transform input data\n  if (Array.isArray(input)) {\n    return input.map(item => ({\n      ...item,\n      transformed: true\n    }));\n  }\n  return { ...input, transformed: true };\n}", "timeout": 60}'::jsonb,
 'code', ARRAY['javascript', 'js', 'transform', 'map']),

-- Python Node Templates
('py-process', 'Python Processor', 'Process input data using Python', 'python', 'TASK',
 '{"code": "def handler(input):\n    # Process the input data\n    result = {\n        \"processed\": True,\n        \"data\": input,\n        \"timestamp\": __import__(\"datetime\").datetime.now().isoformat()\n    }\n    return result", "timeout": 60}'::jsonb,
 'code', ARRAY['python', 'py', 'process', 'transform']),

('py-calculate', 'Python Calculator', 'Perform calculations using Python', 'python', 'TASK',
 '{"code": "def handler(input):\n    # Perform calculations\n    if isinstance(input, dict) and \"a\" in input and \"b\" in input:\n        return {\n            \"sum\": input[\"a\"] + input[\"b\"],\n            \"product\": input[\"a\"] * input[\"b\"],\n            \"difference\": input[\"a\"] - input[\"b\"]\n        }\n    return {\"error\": \"Invalid input\"}", "timeout": 60}'::jsonb,
 'code', ARRAY['python', 'py', 'calculate', 'math']),

-- SQL Node Templates
('sql-select', 'SQL SELECT Query', 'Execute a SELECT query to fetch data from database', 'sql', 'TASK',
 '{"query": "SELECT * FROM users WHERE status = ? LIMIT 100", "parameters": ["active"], "connection_string": "postgresql://user:pass@localhost:5432/dbname"}'::jsonb,
 'database', ARRAY['sql', 'database', 'select', 'query']),

('sql-insert', 'SQL INSERT Query', 'Insert data into database table', 'sql', 'TASK',
 '{"query": "INSERT INTO users (name, email, status) VALUES (?, ?, ?) RETURNING id", "parameters": ["{{input.name}}", "{{input.email}}", "active"], "connection_string": "postgresql://user:pass@localhost:5432/dbname"}'::jsonb,
 'database', ARRAY['sql', 'database', 'insert', 'create']),

-- S3 Node Templates
('s3-upload', 'S3 Upload', 'Upload file to S3 bucket', 's3', 'TASK',
 '{"operation": "upload", "bucket": "my-bucket", "key": "uploads/{{input.filename}}", "data": "{{input.data}}", "region": "us-east-1"}'::jsonb,
 'storage', ARRAY['s3', 'aws', 'upload', 'storage']),

('s3-download', 'S3 Download', 'Download file from S3 bucket', 's3', 'TASK',
 '{"operation": "download", "bucket": "my-bucket", "key": "{{input.key}}", "region": "us-east-1"}'::jsonb,
 'storage', ARRAY['s3', 'aws', 'download', 'storage']),

('s3-list', 'S3 List Objects', 'List objects in S3 bucket', 's3', 'TASK',
 '{"operation": "list", "bucket": "my-bucket", "key": "prefix/", "region": "us-east-1"}'::jsonb,
 'storage', ARRAY['s3', 'aws', 'list', 'storage']),

-- CSV Node Templates
('csv-parse', 'CSV Parser', 'Parse CSV data to JSON', 'csv', 'TASK',
 '{"operation": "parse", "data": "{{input.csv_data}}", "has_header": true, "delimiter": ","}'::jsonb,
 'data', ARRAY['csv', 'parse', 'data', 'transform']),

('csv-filter', 'CSV Filter', 'Filter CSV rows based on column value', 'csv', 'TASK',
 '{"operation": "filter", "data": "{{input.csv_data}}", "has_header": true, "delimiter": ",", "filter_col": "status", "filter_val": "active"}'::jsonb,
 'data', ARRAY['csv', 'filter', 'data', 'transform']);

-- Insert sample workflow templates

INSERT INTO workflow_templates (id, name, description, spec_json, category, tags) VALUES
('simple-api-pipeline', 'Simple API Pipeline', 'A basic workflow that fetches data from an API and processes it',
 '{
   "nodes": [
     {
       "node_id": "fetch_data",
       "name": "Fetch Data",
       "type": "TASK",
       "runtime": "http",
       "spec": {
         "url": "https://api.example.com/data",
         "method": "GET",
         "timeout_seconds": 30
       }
     },
     {
       "node_id": "process_data",
       "name": "Process Data",
       "type": "TASK",
       "runtime": "javascript",
       "spec": {
         "code": "function handler(input) {\n  const data = input || {};\n  return {\n    processed: true,\n    result: data,\n    timestamp: new Date().toISOString()\n  };\n}",
         "timeout": 60
       }
     }
   ],
   "edges": [
     {
       "from": "fetch_data",
       "to": "process_data"
     }
   ]
 }'::jsonb,
 'pipeline', ARRAY['api', 'pipeline', 'simple', 'http', 'javascript']),

('data-transform-pipeline', 'Data Transform Pipeline', 'Fetch data, transform it, and store results',
 '{
   "nodes": [
     {
       "node_id": "fetch",
       "name": "Fetch Data",
       "type": "TASK",
       "runtime": "http",
       "spec": {
         "url": "https://api.example.com/source",
         "method": "GET",
         "timeout_seconds": 30
       }
     },
     {
       "node_id": "transform",
       "name": "Transform",
       "type": "TASK",
       "runtime": "javascript",
       "spec": {
         "code": "function handler(input) {\n  if (Array.isArray(input)) {\n    return input.map(item => ({\n      ...item,\n      transformed: true,\n      processed_at: new Date().toISOString()\n    }));\n  }\n  return { ...input, transformed: true };\n}",
         "timeout": 60
       }
     },
     {
       "node_id": "store",
       "name": "Store Result",
       "type": "TASK",
       "runtime": "s3",
       "spec": {
         "operation": "upload",
         "bucket": "processed-data",
         "key": "results/{{tasks.fetch.output.timestamp}}.json",
         "data": "{{tasks.transform.output}}",
         "region": "us-east-1"
       }
     }
   ],
   "edges": [
     {
       "from": "fetch",
       "to": "transform"
     },
     {
       "from": "transform",
       "to": "store"
     }
   ]
 }'::jsonb,
 'pipeline', ARRAY['transform', 'pipeline', 'etl', 'http', 'javascript', 's3']),

('parallel-processing', 'Parallel Processing', 'Process multiple data sources in parallel',
 '{
   "nodes": [
     {
       "node_id": "fetch_source1",
       "name": "Fetch Source 1",
       "type": "TASK",
       "runtime": "http",
       "spec": {
         "url": "https://api.example.com/source1",
         "method": "GET",
         "timeout_seconds": 30
       }
     },
     {
       "node_id": "fetch_source2",
       "name": "Fetch Source 2",
       "type": "TASK",
       "runtime": "http",
       "spec": {
         "url": "https://api.example.com/source2",
         "method": "GET",
         "timeout_seconds": 30
       }
     },
     {
       "node_id": "merge",
       "name": "Merge Results",
       "type": "TASK",
       "runtime": "javascript",
       "spec": {
         "code": "function handler(input) {\n  return {\n    source1: input.source1 || null,\n    source2: input.source2 || null,\n    merged_at: new Date().toISOString()\n  };\n}",
         "timeout": 60
       }
     }
   ],
   "edges": [
     {
       "from": "fetch_source1",
       "to": "merge"
     },
     {
       "from": "fetch_source2",
       "to": "merge"
     }
   ]
 }'::jsonb,
 'parallel', ARRAY['parallel', 'merge', 'http', 'javascript']),

('csv-processing', 'CSV Processing Workflow', 'Download CSV, parse it, filter rows, and process',
 '{
   "nodes": [
     {
       "node_id": "fetch_csv",
       "name": "Fetch CSV",
       "type": "TASK",
       "runtime": "http",
       "spec": {
         "url": "https://example.com/data.csv",
         "method": "GET",
         "timeout_seconds": 30
       }
     },
     {
       "node_id": "parse_csv",
       "name": "Parse CSV",
       "type": "TASK",
       "runtime": "csv",
       "spec": {
         "operation": "parse",
         "data": "{{tasks.fetch_csv.output}}",
         "has_header": true,
         "delimiter": ","
       }
     },
     {
       "node_id": "filter_csv",
       "name": "Filter CSV",
       "type": "TASK",
       "runtime": "csv",
       "spec": {
         "operation": "filter",
         "data": "{{tasks.parse_csv.output}}",
         "has_header": true,
         "delimiter": ",",
         "filter_col": "status",
         "filter_val": "active"
       }
     },
     {
       "node_id": "process_results",
       "name": "Process Results",
       "type": "TASK",
       "runtime": "javascript",
       "spec": {
         "code": "function handler(input) {\n  const data = input || [];\n  return {\n    count: data.length,\n    items: data,\n    processed_at: new Date().toISOString()\n  };\n}",
         "timeout": 60
       }
     }
   ],
   "edges": [
     {
       "from": "fetch_csv",
       "to": "parse_csv"
     },
     {
       "from": "parse_csv",
       "to": "filter_csv"
     },
     {
       "from": "filter_csv",
       "to": "process_results"
     }
   ]
 }'::jsonb,
 'data', ARRAY['csv', 'data', 'processing', 'filter', 'http', 'javascript']);
