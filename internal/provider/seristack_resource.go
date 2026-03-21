// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"fmt"

	opsyseristackaction "github.com/TechXploreLabs/seristack/pkg/opsy"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &SeristackResource{}
var _ resource.ResourceWithConfigure = &SeristackResource{}

var _ resource.ResourceWithModifyPlan = &SeristackResource{}

func (r *SeristackResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
    if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
        return
    }

    var state, plan SeristackResourceModel
    resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
    resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
    if resp.Diagnostics.HasError() {
        return
    }

    // No update_stack → any change forces replacement
    if plan.UpdateStack.IsNull() || plan.UpdateStack.ValueString() == "" {
        if !plan.Vars.Equal(state.Vars) {
            resp.RequiresReplace = append(resp.RequiresReplace, path.Root("vars"))
        }
        return
    }

    // User-declared immutable keys
    if !plan.RecreateOn.IsNull() && !plan.RecreateOn.IsUnknown() {
        var keys []string
        resp.Diagnostics.Append(plan.RecreateOn.ElementsAs(ctx, &keys, false)...)

        var stateVars, planVars map[string]string
        resp.Diagnostics.Append(state.Vars.ElementsAs(ctx, &stateVars, false)...)
        resp.Diagnostics.Append(plan.Vars.ElementsAs(ctx, &planVars, false)...)

        for _, key := range keys {
            if stateVars[key] != planVars[key] {
                resp.RequiresReplace = append(resp.RequiresReplace, path.Root("vars"))
                return
            }
        }
    }
}

func NewSeristackResource() resource.Resource {
	return &SeristackResource{}
}

// SeristackResource defines the resource implementation.
type SeristackResource struct{}

// SeristackResourceModel describes the resource data model.
type SeristackResourceModel struct {
	ID         types.String `tfsdk:"id"`
	ConfigFile types.String `tfsdk:"configfile"`
	Vars       types.Map    `tfsdk:"vars"`
	Output     types.String `tfsdk:"output"` // JSON string of stack output

	// Per-lifecycle stack names
	CreateStack types.String `tfsdk:"create_stack"`
	ReadStack   types.String `tfsdk:"read_stack"`
	UpdateStack types.String `tfsdk:"update_stack"`
	DeleteStack types.String `tfsdk:"delete_stack"`
}

func (r *SeristackResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_seristack"
}

func (r *SeristackResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a lifecycle backed by seristack stack execution.",

		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Resource identifier, derived from stack output or auto-generated.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"configfile": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Path to the seristack configuration file.",
			},
			"create_stack": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Name of the seristack stack to run on Create.",
			},
			"read_stack": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Name of the seristack stack to run on Read.",
			},
			"update_stack": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Name of the seristack stack to run on Update. If omitted, changes force replacement.",
			},
			"delete_stack": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Name of the seristack stack to run on Delete.",
			},
			"vars": schema.MapAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Variables to pass to every seristack stack invocation.",
			},
			"output": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "JSON string of the output returned by the last seristack execution.",
			},
		},
	}
}

func (r *SeristackResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	// No provider-level config needed currently; extend if you add a provider client
}

// ---------- helpers ----------

func (r *SeristackResource) readVars(ctx context.Context, data *SeristackResourceModel, diags *resource.CreateResponse) map[string]string {
	vars := make(map[string]string)
	if !data.Vars.IsNull() && !data.Vars.IsUnknown() {
		diags.Diagnostics.Append(data.Vars.ElementsAs(ctx, &vars, false)...)
	}
	return vars
}

// invokeStack calls OpsySeristack and returns the raw Result.
func invokeStack(configFile, stackName string, vars map[string]string) (*opsyseristackaction.Result, error) {
	result, err := opsyseristackaction.OpsySeristack(opsyseristackaction.Config{
		ConfigFile: configFile,
		StackName:  stackName,
		Vars:       vars,
	})
	if err != nil {
		return nil, err
	}
	if !result.Success {
		return result, fmt.Errorf("stack '%s' failed: %s\noutput: %s", result.Name, result.Error, result.Output)
	}
	return result, nil
}

// extractID tries to pull an "id" key out of the stack JSON output.
// Falls back to stackName+timestamp if not found.
func extractIDFromOutput(output string) string {
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(output), &m); err == nil {
		if id, ok := m["id"].(string); ok && id != "" {
			return id
		}
	}
	return ""
}

// ---------- CRUD ----------

func (r *SeristackResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data SeristackResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
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

	result, err := invokeStack(data.ConfigFile.ValueString(), data.CreateStack.ValueString(), vars)
	if err != nil {
		resp.Diagnostics.AddError("Seristack Create Error", err.Error())
		return
	}

	id := extractIDFromOutput(result.Output)
	if id == "" {
		resp.Diagnostics.AddError(
			"Seristack Create Error",
			fmt.Sprintf("Create stack '%s' must return JSON with an 'id' field. Got output: %s", result.Name, result.Output),
		)
		return
	}

	data.ID = types.StringValue(id)
	data.Output = types.StringValue(result.Output)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *SeristackResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data SeristackResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
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
	// Inject current ID so the read stack knows what to fetch
	vars["id"] = data.ID.ValueString()

	result, err := invokeStack(data.ConfigFile.ValueString(), data.ReadStack.ValueString(), vars)
	if err != nil {
		resp.Diagnostics.AddError("Seristack Read Error", err.Error())
		return
	}

	data.Output = types.StringValue(result.Output)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *SeristackResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data SeristackResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state SeristackResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if data.UpdateStack.IsNull() || data.UpdateStack.ValueString() == "" {
		resp.Diagnostics.AddError("Seristack Update Error", "update_stack is required when updating. Add it or rely on ForceNew.")
		return
	}

	vars := make(map[string]string)
	if !data.Vars.IsNull() && !data.Vars.IsUnknown() {
		resp.Diagnostics.Append(data.Vars.ElementsAs(ctx, &vars, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}
	vars["id"] = state.ID.ValueString()

	result, err := invokeStack(data.ConfigFile.ValueString(), data.UpdateStack.ValueString(), vars)
	if err != nil {
		resp.Diagnostics.AddError("Seristack Update Error", err.Error())
		return
	}

	data.ID = state.ID
	data.Output = types.StringValue(result.Output)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *SeristackResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data SeristackResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	vars := make(map[string]string)
	if !data.Vars.IsNull() && !data.Vars.IsUnknown() {
		resp.Diagnostics.Append(data.Vars.ElementsAs(ctx, &vars, false)...)
	}
	vars["id"] = data.ID.ValueString()

	_, err := invokeStack(data.ConfigFile.ValueString(), data.DeleteStack.ValueString(), vars)
	if err != nil {
		resp.Diagnostics.AddError("Seristack Delete Error", err.Error())
	}
}