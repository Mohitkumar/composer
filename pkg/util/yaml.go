package util

import (
	"encoding/json"
	"strings"

	"gopkg.in/yaml.v3"
)

// convertToYAMLFriendly recursively processes the object to ensure multiline strings
// are properly formatted for YAML literal block scalars. It specifically handles
// code fields in node specs to ensure they use literal block scalars.
func convertToYAMLFriendly(obj interface{}) interface{} {
	switch v := obj.(type) {
	case map[string]interface{}:
		result := make(map[string]interface{})
		for k, val := range v {
			// Special handling for node specs - ensure code fields are properly formatted
			if k == "spec" {
				if specMap, ok := val.(map[string]interface{}); ok {
					processedSpec := make(map[string]interface{})
					for specKey, specVal := range specMap {
						if specKey == "code" {
							// Code field - ensure newlines are actual newlines, not escaped
							if codeStr, ok := specVal.(string); ok {
								// Replace escaped newlines with actual newlines
								codeStr = strings.ReplaceAll(codeStr, "\\n", "\n")
								// Replace escaped tabs with actual tabs
								codeStr = strings.ReplaceAll(codeStr, "\\t", "\t")
								processedSpec[specKey] = codeStr
							} else {
								processedSpec[specKey] = convertToYAMLFriendly(specVal)
							}
						} else {
							processedSpec[specKey] = convertToYAMLFriendly(specVal)
						}
					}
					result[k] = processedSpec
				} else {
					result[k] = convertToYAMLFriendly(val)
				}
			} else {
				result[k] = convertToYAMLFriendly(val)
			}
		}
		return result
	case []interface{}:
		result := make([]interface{}, len(v))
		for i, val := range v {
			result[i] = convertToYAMLFriendly(val)
		}
		return result
	case string:
		// For any string with escaped newlines, convert them to actual newlines
		// This helps YAML marshaler use literal block scalars
		if strings.Contains(v, "\\n") {
			// Replace escaped newlines with actual newlines
			v = strings.ReplaceAll(v, "\\n", "\n")
			// Replace escaped tabs with actual tabs
			v = strings.ReplaceAll(v, "\\t", "\t")
			return v
		}
		return v
	default:
		return v
	}
}

// setLiteralStyle recursively sets literal block scalar style for strings containing newlines.
// It specifically targets code fields in node specs to use literal block scalars.
func setLiteralStyle(node *yaml.Node) {
	if node == nil {
		return
	}

	// Handle DocumentNode - process its content
	if node.Kind == yaml.DocumentNode {
		for _, child := range node.Content {
			setLiteralStyle(child)
		}
		return
	}

	// Special handling for maps - look for "code" keys and "spec" keys
	if node.Kind == yaml.MappingNode {
		for i := 0; i < len(node.Content)-1; i += 2 {
			keyNode := node.Content[i]
			valueNode := node.Content[i+1]

			if keyNode == nil || valueNode == nil {
				continue
			}

			// If this is a "code" key, always use literal style
			if keyNode.Kind == yaml.ScalarNode && keyNode.Value == "code" {
				if valueNode.Kind == yaml.ScalarNode {
					// Always use literal style for code fields
					valueNode.Style = yaml.LiteralStyle
					valueNode.Tag = "!!str"
				}
			} else if keyNode.Kind == yaml.ScalarNode && keyNode.Value == "spec" {
				// If this is a "spec" key, look for "code" inside it
				if valueNode.Kind == yaml.MappingNode {
					for j := 0; j < len(valueNode.Content)-1; j += 2 {
						specKeyNode := valueNode.Content[j]
						specValueNode := valueNode.Content[j+1]
						if specKeyNode != nil && specKeyNode.Kind == yaml.ScalarNode && specKeyNode.Value == "code" {
							if specValueNode != nil && specValueNode.Kind == yaml.ScalarNode {
								// Always use literal style for code fields in specs
								specValueNode.Style = yaml.LiteralStyle
								specValueNode.Tag = "!!str"
							}
						} else {
							// Recursively process other spec fields
							setLiteralStyle(specValueNode)
						}
					}
				} else {
					setLiteralStyle(valueNode)
				}
			} else {
				// Recursively process the value
				setLiteralStyle(valueNode)
			}
		}
		return
	}

	// If this is a scalar (string) with newlines, use literal block style
	if node.Kind == yaml.ScalarNode && strings.Contains(node.Value, "\n") {
		node.Style = yaml.LiteralStyle
		node.Tag = "!!str"
		return
	}

	// Recursively process children for other node types (SequenceNode, etc.)
	for _, child := range node.Content {
		setLiteralStyle(child)
	}
}

// JSONToYAML converts a JSON spec (as json.RawMessage) to YAML string,
// ensuring multiline strings (especially code fields) are formatted as literal block scalars.
func JSONToYAML(jsonSpec json.RawMessage) (string, error) {
	// Unmarshal JSON to interface{}
	var specObj interface{}
	if err := json.Unmarshal(jsonSpec, &specObj); err != nil {
		return "", err
	}

	return ObjectToYAML(specObj)
}

// ObjectToYAML converts an object to YAML string, ensuring multiline strings
// are formatted as literal block scalars. It uses yaml.Node to have better control
// over formatting, especially for code fields.
func ObjectToYAML(obj interface{}) (string, error) {
	// Process object to handle multiline strings
	processed := convertToYAMLFriendly(obj)

	// Convert to yaml.Node for better control
	var node yaml.Node
	if err := node.Encode(processed); err != nil {
		return "", err
	}

	// The Encode method creates a DocumentNode, we need to process its content
	// Recursively set literal block scalar style for strings with newlines
	if node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		setLiteralStyle(node.Content[0])
	} else {
		setLiteralStyle(&node)
	}

	// Marshal the node to YAML string
	// Use Marshal instead of Encoder to preserve node styles
	yamlBytes, err := yaml.Marshal(&node)
	if err != nil {
		return "", err
	}

	yamlStr := string(yamlBytes)
	// Remove trailing newline
	yamlStr = strings.TrimSuffix(yamlStr, "\n")

	return yamlStr, nil
}

// YAMLToJSON converts a YAML string to JSON (as json.RawMessage).
// This is used when receiving YAML from the frontend and storing as JSON in the database.
func YAMLToJSON(yamlStr string) (json.RawMessage, error) {
	// Parse YAML to interface{}
	var yamlObj interface{}
	if err := yaml.Unmarshal([]byte(yamlStr), &yamlObj); err != nil {
		return nil, err
	}

	// Convert to JSON
	jsonBytes, err := json.Marshal(yamlObj)
	if err != nil {
		return nil, err
	}

	return json.RawMessage(jsonBytes), nil
}
