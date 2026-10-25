// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"

	opsyseristackaction "github.com/TechXploreLabs/seristack/pkg/opsy"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	ephschema "github.com/hashicorp/terraform-plugin-framework/ephemeral/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

var _ ephemeral.EphemeralResource = &SeristackEphemeral{}
var _ ephemeral.EphemeralResourceWithConfigure = &SeristackEphemeral{}

type SeristackEphemeral struct {
	scripts map[string]*opsyseristackaction.Config
}

func NewSeristackEphemeral() ephemeral.EphemeralResource {
	return &SeristackEphemeral{}
}

type SeristackEphemeralModel struct {
	Type      types.String  `tfsdk:"type"`
	Vars      types.Map     `tfsdk:"vars"`
	Sensitive types.Map     `tfsdk:"sensitive"`
	Output    types.Dynamic `tfsdk:"output"`
}

func (e *SeristackEphemeral) Metadata(_ context.Context, req ephemeral.MetadataRequest, resp *ephemeral.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_seristack"
}

func (e *SeristackEphemeral) Schema(_ context.Context, _ ephemeral.SchemaRequest, resp *ephemeral.SchemaResponse) {
	resp.Schema = ephschema.Schema{
		MarkdownDescription: "Runs the `OPEN` stack of a seristack YAML definition and returns its output. " +
			"Nothing is stored in state or plan files. Requires Terraform 1.10 or later.",

		Attributes: map[string]ephschema.Attribute{
			"type": ephschema.StringAttribute{
				Required: true,
				MarkdownDescription: "Type name. Maps to `<type>.yaml` in the configured `local` source. " +
					"The YAML must define an `OPEN` stack.",
			},
			"vars": ephschema.MapAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Variables passed to the `OPEN` stack. Values must be strings; use `jsonencode()` for lists and objects.",
			},
			"sensitive": ephschema.MapAttribute{
				Optional:            true,
				Sensitive:           true,
				ElementType:         types.StringType,
				MarkdownDescription: "Sensitive variables (tokens, passwords, keys), merged on top of `vars`.",
			},
			"output": ephschema.DynamicAttribute{
				Computed:            true,
				Sensitive:           true,
				MarkdownDescription: "The `output` object printed by the `OPEN` stack.",
			},
		},
	}
}

func (e *SeristackEphemeral) Configure(ctx context.Context, req ephemeral.ConfigureRequest, resp *ephemeral.ConfigureResponse) {
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
			"The seristack ephemeral resource requires a `local` block (with `path` or `folder_path`) in the opsy provider configuration.",
		)
		return
	}

	e.scripts = provider.scripts
}

func (e *SeristackEphemeral) Open(ctx context.Context, req ephemeral.OpenRequest, resp *ephemeral.OpenResponse) {
	var config SeristackEphemeralModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	typeName := config.Type.ValueString()

	def, ok := e.scripts[typeName]
	if !ok {
		keys := make([]string, 0, len(e.scripts))
		for k := range e.scripts {
			keys = append(keys, k)
		}
		resp.Diagnostics.AddError("Seristack Type Error",
			fmt.Sprintf("type %q not found — available: %v", typeName, keys))
		return
	}

	vars := mergeVars(ctx, config.Vars, config.Sensitive)

	tflog.Debug(ctx, "Seristack Open: running stack",
		map[string]any{"type": typeName, "stack": "OPEN"})

	result, err := opsyseristackaction.OpsySeristack(&opsyseristackaction.Config{
		Config:    def.Config,
		StackName: "OPEN",
		Vars:      vars,
		Format:    "json",
	})
	if err != nil {
		resp.Diagnostics.AddError("Seristack Open Error", err.Error())
		return
	}
	if !result.Success {
		// Do not print result.Output here: it may contain secrets.
		resp.Diagnostics.AddError("Seristack Open Execution Failed",
			fmt.Sprintf("stack: %q\nerror: %s", result.Name, result.Error))
		return
	}

	output, diags := extractOutputFromOutput(result.Output)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	config.Output = output
	resp.Diagnostics.Append(resp.Result.Set(ctx, &config)...)
}
