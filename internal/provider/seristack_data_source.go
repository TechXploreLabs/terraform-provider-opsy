package provider

import (
	"context"

	opsyseristackaction "github.com/TechXploreLabs/seristack/pkg/opsy"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &SeristackDataSource{}

func NewSeristackDataSource() datasource.DataSource {
	return &SeristackDataSource{}
}

type SeristackDataSource struct{}

type SeristackDataSourceModel struct {
	ConfigFile types.String `tfsdk:"configfile"`
	StackName  types.String `tfsdk:"stackname"`
	Vars       types.Map    `tfsdk:"vars"`
	Output     types.String `tfsdk:"output"`
	ID         types.String `tfsdk:"id"`
}

func (d *SeristackDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_seristack"
}

func (d *SeristackDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Runs a seristack stack and exposes the output as data.",

		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Identifier derived from stack output or stack name.",
			},
			"configfile": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Path to the seristack configuration file.",
			},
			"stackname": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Name of the seristack stack to run.",
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
	var data SeristackDataSourceModel
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

	result, err := opsyseristackaction.OpsySeristack(opsyseristackaction.Config{
		ConfigFile: data.ConfigFile.ValueString(),
		StackName:  data.StackName.ValueString(),
		Vars:       vars,
	})
	if err != nil {
		resp.Diagnostics.AddError("Seristack DataSource Error", err.Error())
		return
	}
	if !result.Success {
		resp.Diagnostics.AddError(
			"Seristack DataSource Execution Failed",
			"stack: '"+result.Name+"'\nerror: "+result.Error+"\noutput: "+result.Output,
		)
		return
	}

	id := extractIDFromOutput(result.Output)
	if id == "" {
		id = data.StackName.ValueString() // fallback
	}

	data.ID = types.StringValue(id)
	data.Output = types.StringValue(result.Output)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}