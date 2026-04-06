package provider

import (
	"context"
	"fmt"

	opsyseristackaction "github.com/TechXploreLabs/seristack/pkg/opsy"
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
	scripts map[string]*opsyseristackaction.Config
}

type SeristackDataSourceModel struct {
	Type   types.String `tfsdk:"type"`
	Vars   types.Map    `tfsdk:"vars"`
	Output types.String `tfsdk:"output"`
	ID     types.String `tfsdk:"id"`
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

	if provider.scripts == nil {
		resp.Diagnostics.AddError(
			"Opsy Provider Not Configured",
			"The seristack data source was initialised before the provider successfully loaded its scripts bundle. Ensure the provider configure step completed without errors.",
		)
		return
	}

	d.scripts = provider.scripts
	tflog.Debug(ctx, "SeristackDatasource configured", map[string]any{"types_available": len(provider.scripts)})
}

func (d *SeristackDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads data via a seristack YAML definition. " +
			"The YAML is fetched from the zip bundle configured in the provider block, " +
			"held in memory for the duration of the run, and never written to disk.",

		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Identifier derived from stack output or stack name.",
			},
			"type": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "Datasource type name. Maps to `<type>.yaml` inside the zip bundle " +
					"(e.g. `bucket` → `bucket.yaml`).",
			},
			"vars": schema.MapAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Variables to pass to the stack.",
			},
			"output": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Raw JSON string output from the stack execution.",
			},
		},
	}
}

func (d *SeristackDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	data, ok := extractModel[SeristackDataSourceModel](ctx, req.Config.Get, &resp.Diagnostics)
	if !ok {
		return
	}

	vars := flattenVars(data.Vars)

	def, err := d.resolveType(data.Type.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Seristack Type Error", err.Error())
		return
	}

	tflog.Debug(ctx, "Seristack DataSource: running stack",
		map[string]any{"type": data.Type.ValueString(), "stack": "DATASOURCE"})

	result, err := opsyseristackaction.OpsySeristack(&opsyseristackaction.Config{
		Config:    def.Config,
		StackName: "DATASOURCE",
		Vars:      vars,
		Format:    "json",
	})

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
	output := extractOutputFromOutput(result.Output)
	data.ID = types.StringValue(id)
	data.Output = types.StringValue(output)
	resp.Diagnostics.Append(resp.State.Set(ctx, data)...)
}

func (r *SeristackDataSource) resolveType(typeName string) (*opsyseristackaction.Config, error) {
	if r.scripts == nil {
		return nil, fmt.Errorf("provider scripts not initialised — check provider configuration")
	}
	def, ok := r.scripts[typeName]
	if !ok {
		keys := make([]string, 0, len(r.scripts))
		for k := range r.scripts {
			keys = append(keys, k)
		}
		return nil, fmt.Errorf("type %q not found in scripts bundle — available: %v", typeName, keys)
	}
	return def, nil
}
