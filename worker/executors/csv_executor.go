package executors

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"strings"
)

// CSVExecutor executes CSV processing operations
type CSVExecutor struct{}

// NewCSVExecutor creates a new CSV executor
func NewCSVExecutor() *CSVExecutor {
	return &CSVExecutor{}
}

// Type returns the executor type
func (e *CSVExecutor) Type() string {
	return "csv"
}

// Execute executes CSV processing based on input JSON
// Expected input format:
//
//	{
//	  "operation": "parse|to_json|filter",
//	  "data": "csv,string,data\nrow1,col1,col2",
//	  "has_header": true,
//	  "delimiter": ","
//	}
func (e *CSVExecutor) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var params struct {
		Operation string `json:"operation"`
		Data      string `json:"data"`
		HasHeader bool   `json:"has_header"`
		Delimiter string `json:"delimiter,omitempty"`
		FilterCol string `json:"filter_col,omitempty"`
		FilterVal string `json:"filter_val,omitempty"`
	}

	if err := json.Unmarshal(input, &params); err != nil {
		return nil, fmt.Errorf("failed to parse CSV params: %w", err)
	}

	if params.Data == "" {
		return nil, fmt.Errorf("data is required")
	}

	delimiter := ','
	if params.Delimiter != "" && len(params.Delimiter) == 1 {
		delimiter = rune(params.Delimiter[0])
	}

	reader := csv.NewReader(strings.NewReader(params.Data))
	reader.Comma = delimiter

	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("failed to parse CSV: %w", err)
	}

	if len(records) == 0 {
		return json.RawMessage(`[]`), nil
	}

	var headers []string
	startIdx := 0

	if params.HasHeader && len(records) > 0 {
		headers = records[0]
		startIdx = 1
	}

	switch params.Operation {
	case "parse", "to_json":
		// Convert CSV to JSON array of objects
		var result []map[string]string
		for i := startIdx; i < len(records); i++ {
			record := make(map[string]string)
			for j, val := range records[i] {
				key := fmt.Sprintf("col%d", j)
				if j < len(headers) {
					key = headers[j]
				}
				record[key] = val
			}
			result = append(result, record)
		}
		return json.Marshal(result)

	case "filter":
		if params.FilterCol == "" {
			return nil, fmt.Errorf("filter_col is required for filter operation")
		}
		var result []map[string]string
		colIdx := -1

		// Find column index
		if params.HasHeader {
			for i, h := range headers {
				if h == params.FilterCol {
					colIdx = i
					break
				}
			}
		} else {
			// Try to parse as column index
			var idx int
			if _, err := fmt.Sscanf(params.FilterCol, "col%d", &idx); err == nil {
				colIdx = idx
			}
		}

		if colIdx < 0 {
			return nil, fmt.Errorf("column %s not found", params.FilterCol)
		}

		for i := startIdx; i < len(records); i++ {
			if colIdx < len(records[i]) && records[i][colIdx] == params.FilterVal {
				record := make(map[string]string)
				for j, val := range records[i] {
					key := fmt.Sprintf("col%d", j)
					if j < len(headers) {
						key = headers[j]
					}
					record[key] = val
				}
				result = append(result, record)
			}
		}
		return json.Marshal(result)

	default:
		return nil, fmt.Errorf("unknown CSV operation: %s", params.Operation)
	}
}
