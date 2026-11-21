// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

var _ datasource.DataSource = &SeristackDataSource{}
var _ datasource.DataSourceWithConfigure = &SeristackDataSource{}

func NewSeristackDataSource() datasource.DataSource {
	return &SeristackDataSource{}
}

type SeristackDataSource struct {
	runner StackRunner
}

type SeristackDataSourceModel struct {
	Type   types.String  `tfsdk:"type"`
	Vars   types.Map     `tfsdk:"vars"`
	Output types.Dynamic `tfsdk:"output"`
	ID     types.String  `tfsdk:"id"`
}

func (d *SeristackDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_seristack"
}

func (d *SeristackDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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
			"The seristack data source requires either a `local` block (with `path` or `folder_path`) "+
				"or an `http` block (with `url`) in the provider configuration.",
		)
		return
	}

	d.runner = provider.runner
	tflog.Debug(ctx, "SeristackDatasource configured")
}

func (d *SeristackDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads data via the `DATASOURCE` stack of a seristack type. " +
			"With the provider `http` block the call is `GET <url>/<type>_DATASOURCE`.",

		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Identifier derived from stack output or stack name.",
			},
			"type": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "Datasource type name. Maps to `<type>.yaml` (local) or the endpoint " +
					"prefix (http), for example `oci_bucket`.",
			},
			"vars": schema.MapAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Variables to pass to the stack.",
			},
			"output": schema.DynamicAttribute{
				Computed:            true,
				MarkdownDescription: "Structured output returned by the last stack execution.",
			},
		},
	}
}

func (d *SeristackDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	data, ok := extractModel[SeristackDataSourceModel](ctx, req.Config.Get, &resp.Diagnostics)
	if !ok {
		return
	}

	if d.runner == nil {
		resp.Diagnostics.AddError("Opsy Provider Not Configured",
			"The provider needs a `local` or `http` block to run seristack stacks.")
		return
	}

	typeName := data.Type.ValueString()
	vars := flattenVars(data.Vars)

	tflog.Debug(ctx, "Seristack DataSource: running stack",
		map[string]any{"type": typeName, "stack": "DATASOURCE"})

	result, err := d.runner.Run(ctx, typeName, "DATASOURCE", vars)
	if err != nil {
		resp.Diagnostics.AddError("Seristack DataSource Error", err.Error())
		return
	}
	if !result.Success {
		resp.Diagnostics.AddError("Seristack DataSource Execution Failed",
			fmt.Sprintf("stack: %q\nerror: %s\noutput: %s", result.Name, result.Error, result.Output))
		return
	}

	id := extractIDFromOutput(result.Output)
	if id == "" {
		id = result.Name
	}
	output, diags := extractOutputFromOutput(result.Output)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	data.ID = types.StringValue(id)
	data.Output = output
	resp.Diagnostics.Append(resp.State.Set(ctx, data)...)
}
