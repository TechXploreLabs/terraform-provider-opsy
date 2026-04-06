package provider

import (
	"encoding/json"
)

func extractIDFromOutput(output string) string {
	var m map[string]any

	if err := json.Unmarshal([]byte(output), &m); err == nil {
		if id, ok := m["id"].(string); ok && id != "" {
			return id
		}
	}
	return ""
}

func extractOutputFromOutput(output string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(output), &m); err != nil {
		return ""
	}
	raw, ok := m["output"]
	if !ok || raw == nil {
		return ""
	}
	// output is a nested object — marshal it back to a JSON string for storage
	b, err := json.Marshal(raw)
	if err != nil {
		return ""
	}
	return string(b)
}
