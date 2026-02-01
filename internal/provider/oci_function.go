// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var ocidResultAttrTypes = map[string]attr.Type{
	"service": types.StringType,
	"region":  types.StringType,
}

var (
	_ function.Function = OCIFunction{}
)

func NewOCIFunction() function.Function {
	return OCIFunction{}
}

type OCIFunction struct{}

func (r OCIFunction) Metadata(_ context.Context, req function.MetadataRequest, resp *function.MetadataResponse) {
	resp.Name = "oci"
}

func (r OCIFunction) Definition(_ context.Context, _ function.DefinitionRequest, resp *function.DefinitionResponse) {
	resp.Definition = function.Definition{
		Summary: "Provide the OCI OCID",
		Parameters: []function.Parameter{
			function.StringParameter{
				AllowNullValue:     false,
				AllowUnknownValues: false,
				Description:        "OCID of oci resources",
				Name:               "ocid",
			},
		},
		Return: function.ObjectReturn{
			AttributeTypes: ocidResultAttrTypes,
		},
	}
}

func (r OCIFunction) Run(ctx context.Context, req function.RunRequest, resp *function.RunResponse) {
	var ocid string

	resp.Error = function.ConcatFuncErrors(req.Arguments.Get(ctx, &ocid))
	if resp.Error != nil {
		return
	}

	result, err := ocidparse(ocid)
	if err != nil {
		resp.Error = function.ConcatFuncErrors(resp.Error, function.NewFuncError(err.Error()))
		resp.Error = function.ConcatFuncErrors(resp.Result.Set(ctx, false))
	}

	resp.Error = function.ConcatFuncErrors(resp.Result.Set(ctx, result))
}

func ocidparse(ocid string) (types.Object, error) {
	re := regexp.MustCompile(`\.`)
	parts := re.Split(ocid, -1)

	if len(parts) != 5 {
		return types.ObjectNull(ocidResultAttrTypes), fmt.Errorf("not a valid ocid")
	}

	attrValues := map[string]attr.Value{
		"service": types.StringValue(parts[1]),
		"region":  types.StringValue(parts[3]),
	}

	obj, diags := types.ObjectValue(ocidResultAttrTypes, attrValues)
	if diags.HasError() {
		return types.ObjectNull(ocidResultAttrTypes), fmt.Errorf("failed to create object: %v", diags)
	}

	return obj, nil
}
