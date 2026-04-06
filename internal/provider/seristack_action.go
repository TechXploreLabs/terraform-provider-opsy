// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"

	opsyseristackaction "github.com/TechXploreLabs/seristack/pkg/opsy"
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
	scripts map[string]*opsyseristackaction.Config
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
		// This description is used by the documentation generator and the language server.
		MarkdownDescription: "Seristack action for stack execution",

		Attributes: map[string]schema.Attribute{
			"type": schema.StringAttribute{
				MarkdownDescription: "Resource type name. Maps to `<type>.yaml` inside the scripts bundle.",
				Required:            true,
			},
			"stackname": schema.StringAttribute{
				MarkdownDescription: "Stack name to be executed",
				Required:            true,
			},
			"vars": schema.MapAttribute{ // ← Add this
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

	if provider.scripts == nil {
		resp.Diagnostics.AddError(
			"Opsy Provider Not Configured",
			"The seristack action was initialised before the provider successfully loaded its scripts bundle.",
		)
		return
	}

	e.scripts = provider.scripts
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

	vars := make(map[string]string)
	if !data.Vars.IsNull() && !data.Vars.IsUnknown() {
		resp.Diagnostics.Append(data.Vars.ElementsAs(ctx, &vars, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	def, ok := e.scripts[data.Type.ValueString()]
	if !ok {
		resp.Diagnostics.AddError("Seristack Type Error", fmt.Sprintf("type %q not found in scripts bundle", data.Type.ValueString()))
		return
	}

	result, err := opsyseristackaction.OpsySeristack(&opsyseristackaction.Config{
		Config:    def.Config,
		StackName: data.StackName.ValueString(),
		Vars:      vars,
		Format:    "json",
	})

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
		Message: fmt.Sprintf("stack '%s' completed in %s", result.Name, result.Duration),
	})
}
