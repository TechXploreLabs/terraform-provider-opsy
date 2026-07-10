package provider

import (
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
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

func extractVarsFromOutput(output string) (map[string]string, bool, diag.Diagnostics) {
	var diags diag.Diagnostics

	var m map[string]any
	if err := json.Unmarshal([]byte(output), &m); err != nil {
		return nil, false, diags
	}

	raw, ok := m["vars"]
	if !ok || raw == nil {
		return nil, false, diags
	}

	rawVars, ok := raw.(map[string]any)
	if !ok {
		diags.AddError(
			"Invalid Seristack READ vars",
			"The top-level 'vars' field returned by READ must be a JSON object.",
		)
		return nil, false, diags
	}

	vars := make(map[string]string, len(rawVars))
	for k, v := range rawVars {
		switch value := v.(type) {
		case string:
			vars[k] = value
		default:
			b, err := json.Marshal(value)
			if err != nil {
				diags.AddError(
					"Invalid Seristack READ vars",
					fmt.Sprintf("Could not marshal READ vars[%q]: %v", k, err),
				)
				return nil, false, diags
			}
			vars[k] = string(b)
		}
	}

	return vars, true, diags
}
