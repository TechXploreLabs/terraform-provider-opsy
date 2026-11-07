// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/action"
	"github.com/hashicorp/terraform-plugin-framework/action/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ action.Action = &SeristackAction{}
var _ action.ActionWithConfigure = &SeristackAction{}

func NewSeristackAction() action.Action {
	return &SeristackAction{}
}

// SeristackAction defines the action implementation.
type SeristackAction struct {
	runner StackRunner
}

// SeristackActionModel describes the action data model.
type SeristackActionModel struct {
	Type      types.String `tfsdk:"type"`
	StackName types.String `tfsdk:"stackname"`
	Vars      types.Map    `tfsdk:"vars"`
}

func (e *SeristackAction) Metadata(ctx context.Context, req action.MetadataRequest, resp *action.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_seristack"
}

func (e *SeristackAction) Schema(ctx context.Context, req action.SchemaRequest, resp *action.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Runs a named seristack stack. With the provider `local` block the stack is " +
			"looked up in `<type>.yaml`; with the `http` block it is called as `POST <url>/<type>_<stackname>`.",

		Attributes: map[string]schema.Attribute{
			"type": schema.StringAttribute{
				MarkdownDescription: "Resource type name. Maps to `<type>.yaml` (local) or the endpoint prefix (http).",
				Required:            true,
			},
			"stackname": schema.StringAttribute{
				MarkdownDescription: "Stack name to be executed. Over http the stack must be registered on the server with `method: POST`.",
				Required:            true,
			},
			"vars": schema.MapAttribute{
				MarkdownDescription: "Variables to pass to the stack",
				Optional:            true,
				ElementType:         types.StringType,
			},
		},
	}
}

func (e *SeristackAction) Configure(ctx context.Context, req action.ConfigureRequest, resp *action.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	provider, ok := req.ProviderData.(*OpsyProvider)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Provider Data Type",
			fmt.Sprintf("Expected *OpsyProvider, got %T.", req.ProviderData),
		)
		return
	}

	if provider.runner == nil {
		resp.Diagnostics.AddError(
			"Opsy Provider Not Configured",
			"The seristack action requires either a `local` block (with `path` or `folder_path`) "+
				"or an `http` block (with `url`) in the provider configuration.",
		)
		return
	}

	e.runner = provider.runner
}

func (e *SeristackAction) Invoke(ctx context.Context, req action.InvokeRequest, resp *action.InvokeResponse) {
	// Send a progress message back to Terraform
	resp.SendProgress(action.InvokeProgressEvent{
		Message: "starting action invocation",
	})

	var data SeristackActionModel

	// Read Terraform configuration data into the model
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	if e.runner == nil {
		resp.Diagnostics.AddError("Opsy Provider Not Configured",
			"The provider needs a `local` or `http` block to run seristack stacks.")
		return
	}

	vars := make(map[string]string)
	if !data.Vars.IsNull() && !data.Vars.IsUnknown() {
		resp.Diagnostics.Append(data.Vars.ElementsAs(ctx, &vars, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	start := time.Now()
	result, err := e.runner.Run(ctx, data.Type.ValueString(), data.StackName.ValueString(), vars)
	if err != nil {
		resp.Diagnostics.AddError("Seristack Error", err.Error())
		return
	}

	if !result.Success {
		resp.Diagnostics.AddError(
			"Seristack Execution Failed",
			fmt.Sprintf("stack: '%s'\nsuccess: %t\nerror: %s\noutput: %s", result.Name, result.Success, result.Error, result.Output),
		)
		return
	}

	resp.SendProgress(action.InvokeProgressEvent{
		Message: fmt.Sprintf("stack: '%s'\nsuccess: %t\noutput: %s", result.Name, result.Success, result.Output),
	})

	resp.SendProgress(action.InvokeProgressEvent{
		Message: fmt.Sprintf("stack '%s' completed in %s", result.Name, time.Since(start).Round(time.Millisecond)),
	})
}
