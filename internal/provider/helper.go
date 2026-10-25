package provider

import (
	"encoding/json"
	"fmt"
	"math/big"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
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

func extractOutputFromOutput(output string) (types.Dynamic, diag.Diagnostics) {
	var diags diag.Diagnostics

	var m map[string]any
	if err := json.Unmarshal([]byte(output), &m); err != nil {
		diags.AddError("Invalid Seristack Output", err.Error())
		return types.DynamicUnknown(), diags
	}
	raw, ok := m["output"]
	if !ok || raw == nil {
		return types.DynamicNull(), diags
	}

	value, err := convertJSONToTerraformValue(raw)
	if err != nil {
		diags.AddError("Invalid Seristack Output", err.Error())
		return types.DynamicUnknown(), diags
	}
	return types.DynamicValue(value), diags
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

func convertJSONToTerraformValue(v any) (attr.Value, error) {
	switch value := v.(type) {
	case nil:
		return types.DynamicNull(), nil

	case string:
		return types.StringValue(value), nil

	case bool:
		return types.BoolValue(value), nil

	case float64:
		return types.NumberValue(big.NewFloat(value)), nil

	case map[string]any:
		attrs := make(map[string]attr.Value, len(value))
		attrTypes := make(map[string]attr.Type, len(value))

		for k, v := range value {
			converted, err := convertJSONToTerraformValue(v)
			if err != nil {
				return nil, fmt.Errorf("attribute %q: %w", k, err)
			}

			attrs[k] = converted
			attrTypes[k] = converted.Type(nil)
		}

		obj, diags := types.ObjectValue(attrTypes, attrs)
		if diags.HasError() {
			return nil, fmt.Errorf("could not construct Terraform object: %v", diags)
		}

		return obj, nil

	case []any:
		values := make([]attr.Value, len(value))
		var elementType attr.Type

		for i, item := range value {
			converted, err := convertJSONToTerraformValue(item)
			if err != nil {
				return nil, fmt.Errorf("array index %d: %w", i, err)
			}

			if elementType == nil {
				elementType = converted.Type(nil)
			} else if !elementType.Equal(converted.Type(nil)) {
				return nil, fmt.Errorf("array contains incompatible element types")
			}

			values[i] = converted
		}

		if elementType == nil {
			// An empty JSON array has no inferable element type.
			// Represent it as a list of dynamic values.
			elementType = types.DynamicType
		}

		list, diags := types.ListValue(elementType, values)
		if diags.HasError() {
			return nil, fmt.Errorf("could not construct Terraform list: %v", diags)
		}

		return list, nil

	default:
		return nil, fmt.Errorf("unsupported JSON value type %T", v)
	}
}

func dynamicValueToJSON(value types.Dynamic) (string, error) {
	if value.IsNull() || value.IsUnknown() {
		return "null", nil
	}

	raw := value.UnderlyingValue()
	if raw == nil {
		return "null", nil
	}

	b, err := json.Marshal(raw)
	if err != nil {
		return "", fmt.Errorf("failed to marshal output: %w", err)
	}

	return string(b), nil
}
