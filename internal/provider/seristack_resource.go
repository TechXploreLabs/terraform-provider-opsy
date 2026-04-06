// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"fmt"

	opsyseristackaction "github.com/TechXploreLabs/seristack/pkg/opsy"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

var _ resource.Resource = &SeristackResource{}
var _ resource.ResourceWithConfigure = &SeristackResource{}
var _ resource.ResourceWithModifyPlan = &SeristackResource{}
var _ resource.ResourceWithImportState = &SeristackResource{}

type SeristackResource struct {
	scripts map[string]*opsyseristackaction.Config
}

func NewSeristackResource() resource.Resource {
	return &SeristackResource{}
}

type SeristackResourceModel struct {
	ID        types.String `tfsdk:"id"`
	Type      types.String `tfsdk:"type"`
	Vars      types.Map    `tfsdk:"vars"`
	Sensitive types.Map    `tfsdk:"sensitive"`
	Output    types.String `tfsdk:"output"`
}

type seristackImportData struct {
	ID   string            `json:"id"`
	Type string            `json:"type"`
	Vars map[string]string `json:"vars,omitempty"`
}

func (r *SeristackResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_seristack"
}

func (r *SeristackResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a resource lifecycle driven by a seristack YAML definition. " +
			"The YAML is fetched from the zip bundle configured in the provider block, " +
			"held in memory for the duration of the run, and never written to disk.",

		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Resource identifier returned by the create stack.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"type": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "Resource type name. Maps to `<type>.yaml` inside the zip bundle " +
					"(e.g. `bucket` → `bucket.yaml`). Changing the type always forces replacement.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"vars": schema.MapAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Variables passed to every stack invocation.",
			},
			"sensitive": schema.MapAttribute{
				Optional:    true,
				Sensitive:   true,
				WriteOnly:   true,
				ElementType: types.StringType,
				MarkdownDescription: "Write-only map of sensitive variables " +
					"(tokens, passwords, keys). Merged on top of `vars` at execution time. " +
					"Never stored in state.",
			},
			"output": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "JSON string of the output returned by the last stack execution.",
			},
		},
	}
}

func (r *SeristackResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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
			"The seristack resource was initialised before the provider successfully loaded its scripts bundle. Ensure the provider configure step completed without errors.",
		)
		return
	}

	r.scripts = provider.scripts
	tflog.Debug(ctx, "SeristackResource configured", map[string]any{"types_available": len(provider.scripts)})
}

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

	if plan.Vars.Equal(state.Vars) {
		return
	}

	def, err := r.resolveType(plan.Type.ValueString())
	if err != nil {
		return
	}

	vars := flattenVars(plan.Vars)
	result, stackErr := opsyseristackaction.OpsySeristack(&opsyseristackaction.Config{
		Config:    def.Config,
		StackName: "RECREATE_ON",
		Vars:      vars,
		Format:    "json",
	})
	if stackErr != nil {
		resp.Diagnostics.AddWarning("recreate_on stack execution error",
			fmt.Sprintf("Failed to execute recreate_on stack for type %q: %v", plan.Type.ValueString(), stackErr))
		return
	}
	if !result.Success {
		resp.Diagnostics.AddWarning("recreate_on stack execution failed",
			fmt.Sprintf("Stack %q failed: %s", result.Name, result.Error))
		return
	}

	var recreateKeys []string
	if err := json.Unmarshal([]byte(result.Output), &recreateKeys); err != nil {
		resp.Diagnostics.AddWarning("recreate_on output parse error",
			fmt.Sprintf("Type %q recreate_on stack must return a JSON array of strings, got: %s", plan.Type.ValueString(), result.Output))
		return
	}

	if len(recreateKeys) == 0 {
		return
	}

	var stateVars, planVars map[string]string
	resp.Diagnostics.Append(state.Vars.ElementsAs(ctx, &stateVars, false)...)
	resp.Diagnostics.Append(plan.Vars.ElementsAs(ctx, &planVars, false)...)
	if resp.Diagnostics.HasError() {
		return
	}

	for _, key := range recreateKeys {
		if stateVars[key] != planVars[key] {
			tflog.Debug(ctx, "ModifyPlan: recreate_on key changed → RequiresReplace",
				map[string]any{"type": plan.Type.ValueString(), "key": key})
			resp.RequiresReplace = append(resp.RequiresReplace, path.Root("vars"))
			return
		}
	}
}

func (r *SeristackResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	plan, ok := extractModel[SeristackResourceModel](ctx, req.Plan.Get, &resp.Diagnostics)
	if !ok {
		return
	}
	config, ok := extractModel[SeristackResourceModel](ctx, req.Config.Get, &resp.Diagnostics)
	if !ok {
		return
	}

	def, err := r.resolveType(plan.Type.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Seristack Type Error", err.Error())
		return
	}

	vars := mergeVars(ctx, plan.Vars, config.Sensitive)

	tflog.Debug(ctx, "Seristack Create: running stack",
		map[string]any{"type": plan.Type.ValueString(), "stack": "CREATE"})

	result, err := opsyseristackaction.OpsySeristack(&opsyseristackaction.Config{
		Config:    def.Config,
		StackName: "CREATE",
		Vars:      vars,
		Format:    "json",
	})
	if err != nil {
		resp.Diagnostics.AddError("Seristack Create Error", err.Error())
		return
	}
	if !result.Success {
		resp.Diagnostics.AddError("Seristack Create Execution Failed",
			fmt.Sprintf("stack: %q\nerror: %s\noutput: %s", result.Name, result.Error, result.Output))
		return
	}

	id := extractIDFromOutput(result.Output)
	if id == "" {
		resp.Diagnostics.AddError("Seristack Create Error",
			fmt.Sprintf("Create stack %q must return JSON with an 'id' field. Got: %s", result.Name, result.Output))
		return
	}

	tflog.Debug(ctx, "Seristack Create: success", map[string]any{"id": id})

	plan.ID = types.StringValue(id)
	plan.Output = types.StringValue(extractOutputFromOutput(result.Output))
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *SeristackResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	state, ok := extractModel[SeristackResourceModel](ctx, req.State.Get, &resp.Diagnostics)
	if !ok {
		return
	}

	def, err := r.resolveType(state.Type.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Seristack Type Error", err.Error())
		return
	}

	vars := flattenVars(state.Vars)
	vars["id"] = state.ID.ValueString()
	vars["_output"] = state.Output.ValueString()

	tflog.Debug(ctx, "Seristack Read: running stack",
		map[string]any{"type": state.Type.ValueString(), "stack": "READ", "id": state.ID.ValueString()})

	result, err := opsyseristackaction.OpsySeristack(&opsyseristackaction.Config{
		Config:    def.Config,
		StackName: "READ",
		Vars:      vars,
		Format:    "json",
	})
	if err != nil {
		resp.Diagnostics.AddError("Seristack Read Error", err.Error())
		return
	}
	if result.Success && isNotFound(result.Output) {
		tflog.Info(ctx, "Seristack Read: resource not found upstream, removing from state",
			map[string]any{"id": state.ID.ValueString()})
		resp.State.RemoveResource(ctx)
		return
	}

	if !result.Success {
		resp.Diagnostics.AddError("Seristack Read Execution Failed",
			fmt.Sprintf("stack: %q\nerror: %s\noutput: %s", result.Name, result.Error, result.Output))
		return
	}

	state.Output = types.StringValue(extractOutputFromOutput(result.Output))
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *SeristackResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	plan, ok := extractModel[SeristackResourceModel](ctx, req.Plan.Get, &resp.Diagnostics)
	if !ok {
		return
	}
	state, ok := extractModel[SeristackResourceModel](ctx, req.State.Get, &resp.Diagnostics)
	if !ok {
		return
	}
	config, ok := extractModel[SeristackResourceModel](ctx, req.Config.Get, &resp.Diagnostics)
	if !ok {
		return
	}

	def, err := r.resolveType(plan.Type.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Seristack Type Error", err.Error())
		return
	}

	if state.Vars.Equal(plan.Vars) {
		tflog.Info(ctx, "Seristack Update: vars unchanged, skipping execution")
		plan.ID = state.ID
		plan.Output = state.Output
		resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
		return
	}

	vars := mergeVars(ctx, plan.Vars, config.Sensitive)
	vars["id"] = state.ID.ValueString()
	vars["_output"] = state.Output.ValueString()

	tflog.Debug(ctx, "Seristack Update: running stack",
		map[string]any{"type": plan.Type.ValueString(), "stack": "UPDATE", "id": state.ID.ValueString()})

	result, err := opsyseristackaction.OpsySeristack(&opsyseristackaction.Config{
		Config:    def.Config,
		StackName: "UPDATE",
		Vars:      vars,
		Format:    "json",
	})
	if err != nil {
		resp.Diagnostics.AddError("Seristack Update Error", err.Error())
		return
	}
	if !result.Success {
		resp.Diagnostics.AddError("Seristack Update Execution Failed",
			fmt.Sprintf("stack: %q\nerror: %s\noutput: %s", result.Name, result.Error, result.Output))
		return
	}

	if newID := extractIDFromOutput(result.Output); newID != "" && newID != state.ID.ValueString() {
		tflog.Debug(ctx, "Seristack Update: stack returned new id",
			map[string]any{"old_id": state.ID.ValueString(), "new_id": newID})
		plan.ID = types.StringValue(newID)
	} else {
		plan.ID = state.ID
	}

	plan.Output = types.StringValue(extractOutputFromOutput(result.Output))
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *SeristackResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	state, ok := extractModel[SeristackResourceModel](ctx, req.State.Get, &resp.Diagnostics)
	if !ok {
		return
	}

	def, err := r.resolveType(state.Type.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Seristack Type Error", err.Error())
		return
	}

	vars := flattenVars(state.Vars)
	vars["id"] = state.ID.ValueString()
	vars["_output"] = state.Output.ValueString()

	tflog.Debug(ctx, "Seristack Delete: running stack",
		map[string]any{"type": state.Type.ValueString(), "stack": "DELETE", "id": state.ID.ValueString()})

	result, err := opsyseristackaction.OpsySeristack(&opsyseristackaction.Config{
		Config:    def.Config,
		StackName: "DELETE",
		Vars:      vars,
		Format:    "json",
	})
	if err != nil {
		resp.Diagnostics.AddError("Seristack Delete Error", err.Error())
		return
	}
	if !result.Success {
		resp.Diagnostics.AddError("Seristack Delete Execution Failed",
			fmt.Sprintf("stack: %q\nerror: %s\noutput: %s", result.Name, result.Error, result.Output))
		return
	}

	tflog.Debug(ctx, "Seristack Delete: success", map[string]any{"id": state.ID.ValueString()})
}

func (r *SeristackResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	var imp seristackImportData
	if err := json.Unmarshal([]byte(req.ID), &imp); err != nil {
		resp.Diagnostics.AddError(
			"Invalid Import JSON",
			fmt.Sprintf("Import must be JSON: {\"id\":\"...\",\"type\":\"...\",\"vars\":{...}}. Error: %v", err),
		)
		return
	}

	if imp.ID == "" || imp.Type == "" {
		resp.Diagnostics.AddError("Invalid Import JSON", "Both 'id' and 'type' are required.")
		return
	}

	if imp.Vars == nil {
		imp.Vars = map[string]string{}
	}

	def, err := r.resolveType(imp.Type)
	if err != nil {
		resp.Diagnostics.AddError("Seristack Type Error", err.Error())
		return
	}

	readVars := make(map[string]string, len(imp.Vars)+1)
	for k, v := range imp.Vars {
		readVars[k] = v
	}
	readVars["id"] = imp.ID

	result, err := opsyseristackaction.OpsySeristack(&opsyseristackaction.Config{
		Config:    def.Config,
		StackName: "READ",
		Vars:      readVars,
		Format:    "json",
	})
	if err != nil {
		resp.Diagnostics.AddError("Seristack Import Read Error", err.Error())
		return
	}
	if !result.Success {
		resp.Diagnostics.AddError("Seristack Import Read Execution Failed",
			fmt.Sprintf("stack: %q\nerror: %s\noutput: %s", result.Name, result.Error, result.Output))
		return
	}

	varsValue, diags := types.MapValueFrom(ctx, types.StringType, imp.Vars)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	data := SeristackResourceModel{
		ID:        types.StringValue(imp.ID),
		Type:      types.StringValue(imp.Type),
		Vars:      varsValue,
		Sensitive: types.MapNull(types.StringType),
		Output:    types.StringValue(extractOutputFromOutput(result.Output)),
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *SeristackResource) resolveType(typeName string) (*opsyseristackaction.Config, error) {
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

func extractModel[T any](
	ctx context.Context,
	getFn func(context.Context, any) diag.Diagnostics,
	diagnostics *diag.Diagnostics,
) (*T, bool) {
	var model T
	*diagnostics = append(*diagnostics, getFn(ctx, &model)...)
	if diagnostics.HasError() {
		return nil, false
	}
	return &model, true
}

func mergeVars(_ context.Context, vars types.Map, sensitive types.Map) map[string]string {
	merged := flattenVars(vars)

	if sensitive.IsNull() || sensitive.IsUnknown() {
		return merged
	}
	for k, v := range sensitive.Elements() {
		if sv, ok := v.(types.String); ok {
			merged[k] = sv.ValueString()
		}
	}
	return merged
}

func flattenVars(vars types.Map) map[string]string {
	out := make(map[string]string)
	if vars.IsNull() || vars.IsUnknown() {
		return out
	}
	for k, v := range vars.Elements() {
		if sv, ok := v.(types.String); ok {
			out[k] = sv.ValueString()
		}
	}
	return out
}

func isNotFound(output string) bool {
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(output), &m); err != nil {
		return false
	}
	switch v := m["not_found"].(type) {
	case bool:
		return v
	case string:
		return v == "true"
	}
	return false
}
