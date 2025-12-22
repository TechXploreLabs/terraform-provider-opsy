// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/function"
)

var (
	_ function.Function = GetEnvVarFunction{}
)

func NewGetEnvVarFunction() function.Function {
	return GetEnvVarFunction{}
}

type GetEnvVarFunction struct{}

func (r GetEnvVarFunction) Metadata(_ context.Context, req function.MetadataRequest, resp *function.MetadataResponse) {
	resp.Name = "getenvvar"
}

func (r GetEnvVarFunction) Definition(_ context.Context, _ function.DefinitionRequest, resp *function.DefinitionResponse) {
	resp.Definition = function.Definition{
		Summary: "Checks whether current date and time matches the slot",
		Parameters: []function.Parameter{
			function.StringParameter{
				AllowNullValue:     false,
				AllowUnknownValues: false,
				Description:        "Enviroment variable name",
				Name:               "env_var_name",
			},
		},
		Return: function.StringReturn{},
	}
}

func (r GetEnvVarFunction) Run(ctx context.Context, req function.RunRequest, resp *function.RunResponse) {
	var env_var_name string

	resp.Error = function.ConcatFuncErrors(req.Arguments.Get(ctx, &env_var_name))
	if resp.Error != nil {
		return
	}

	result := os.Getenv(env_var_name)
	resp.Error = function.ConcatFuncErrors(resp.Result.Set(ctx, result))
}
