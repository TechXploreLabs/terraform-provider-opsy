// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/attr"
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
var _ resource.ResourceWithValidateConfig = &SeristackResource{}

const (
	defaultCreateTimeout = 10 * time.Minute
	defaultReadTimeout   = 10 * time.Minute
	defaultUpdateTimeout = 10 * time.Minute
	defaultDeleteTimeout = 10 * time.Minute
)

const taintNote = "\n\nThe resource was saved in state and is marked tainted. The next apply will " +
	"destroy and recreate it, so the DELETE stack must succeed when the resource is already gone."

type SeristackResource struct {
	runner StackRunner
}

func NewSeristackResource() resource.Resource {
	return &SeristackResource{}
}

type SeristackResourceModel struct {
	ID        types.String   `tfsdk:"id"`
	Type      types.String   `tfsdk:"type"`
	Vars      types.Map      `tfsdk:"vars"`
	Sensitive types.Map      `tfsdk:"sensitive"`
	Output    types.Dynamic  `tfsdk:"output"`
	Timeouts  timeouts.Value `tfsdk:"timeouts"`
}

type seristackImportData struct {
	ID   string            `json:"id"`
	Type string            `json:"type"`
	Vars map[string]string `json:"vars,omitempty"`
}

func (r *SeristackResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_seristack"
}

func (r *SeristackResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a resource lifecycle driven by seristack stacks. " +
			"Stacks run either locally (provider `local` block) or on a seristack HTTP server " +
			"(provider `http` block). A CREATE, UPDATE or DELETE stack may answer " +
			"`seristack_status = pending`; the provider then calls the same stack again " +
			"until it reports `succeeded` or `failed` (see `timeouts`). " +
			"Variable names starting with `seristack_` are reserved.",

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
				MarkdownDescription: "Resource type name. With the `local` block it maps to `<type>.yaml`; " +
					"with the `http` block it is the prefix of the stack endpoints (`/<type>_CREATE`, ...). " +
					"Changing the type always forces replacement.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"vars": schema.MapAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Variables passed to every stack invocation. Values must be strings; use `jsonencode()` for lists and objects. Names starting with `seristack_` are reserved.",
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
			"output": schema.DynamicAttribute{
				Computed:            true,
				MarkdownDescription: "Structured output returned by the last stack execution.",
			},
			"timeouts": timeouts.Attributes(ctx, timeouts.Opts{
				Create: true,
				Read:   true,
				Update: true,
				Delete: true,
			}),
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

	if provider.runner == nil {
		resp.Diagnostics.AddError(
			"Opsy Provider Not Configured",
			"The seristack resource requires either a `local` block (with `path` or `folder_path`) "+
				"or an `http` block (with `url`) in the provider configuration.",
		)
		return
	}

	r.runner = provider.runner
	tflog.Debug(ctx, "SeristackResource configured")
}

func withOpTimeout(
	ctx context.Context,
	get func(context.Context, time.Duration) (time.Duration, diag.Diagnostics),
	def time.Duration,
	diags *diag.Diagnostics,
) (context.Context, context.CancelFunc) {
	d, dd := get(ctx, def)
	diags.Append(dd...)
	if diags.HasError() {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, d)
}

func nullTimeouts() timeouts.Value {
	return timeouts.Value{Object: types.ObjectNull(map[string]attr.Type{
		"create": types.StringType,
		"read":   types.StringType,
		"update": types.StringType,
		"delete": types.StringType,
	})}
}

func (r *SeristackResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}
	if r.runner == nil {
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

	typeName := plan.Type.ValueString()
	vars := flattenVars(plan.Vars)

	recreateKeys, err := runKeyListStack(ctx, r.runner, typeName, "RECREATE_ON", vars)
	if err != nil {
		resp.Diagnostics.AddWarning("recreate_on stack execution error",
			fmt.Sprintf("Failed to execute recreate_on stack for type %q: %v", typeName, err))
		return
	}

	if len(recreateKeys) > 0 {
		var stateVars, planVars map[string]string
		resp.Diagnostics.Append(state.Vars.ElementsAs(ctx, &stateVars, false)...)
		resp.Diagnostics.Append(plan.Vars.ElementsAs(ctx, &planVars, false)...)
		if resp.Diagnostics.HasError() {
			return
		}

		for _, key := range recreateKeys {
			if stateVars[key] != planVars[key] {
				tflog.Debug(ctx, "ModifyPlan: recreate_on key changed → RequiresReplace",
					map[string]any{"type": typeName, "key": key})
				resp.RequiresReplace = append(resp.RequiresReplace, path.Root("vars"))
				return
			}
		}
	}

	if !plan.Output.IsUnknown() {
		return
	}

	volatileKeys, err := runKeyListStack(ctx, r.runner, typeName, "OUTPUT_CHANGES_ON", vars)
	if err != nil {
		tflog.Debug(ctx, "ModifyPlan: OUTPUT_CHANGES_ON not available, output stays fully unknown",
			map[string]any{"type": typeName, "error": err.Error()})
		return
	}
	if len(volatileKeys) == 0 {
		return
	}

	partial, diags := partiallyUnknownOutput(ctx, state.Output, volatileKeys)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("output"), partial)...)
}

func runKeyListStack(ctx context.Context, runner StackRunner, typeName, stack string, vars map[string]string) ([]string, error) {
	result, err := runner.Run(ctx, typeName, stack, vars)
	if err != nil {
		return nil, err
	}
	if !result.Success {
		return nil, fmt.Errorf("stack %q failed: %s", result.Name, result.Error)
	}

	var keys []string
	if err := json.Unmarshal([]byte(result.Output), &keys); err != nil {
		return nil, fmt.Errorf("stack %q must return a JSON array of strings, got: %s", stack, result.Output)
	}
	return keys, nil
}

func partiallyUnknownOutput(ctx context.Context, prior types.Dynamic, volatile []string) (types.Dynamic, diag.Diagnostics) {
	var diags diag.Diagnostics

	if prior.IsNull() || prior.IsUnknown() ||
		prior.IsUnderlyingValueNull() || prior.IsUnderlyingValueUnknown() {
		return types.DynamicUnknown(), diags
	}

	obj, ok := prior.UnderlyingValue().(types.Object)
	if !ok {
		return types.DynamicUnknown(), diags
	}

	priorTypes := obj.AttributeTypes(ctx)
	priorAttrs := obj.Attributes()

	newTypes := make(map[string]attr.Type, len(priorTypes))
	newAttrs := make(map[string]attr.Value, len(priorAttrs))
	for k, t := range priorTypes {
		newTypes[k] = t
		newAttrs[k] = priorAttrs[k]
	}

	for _, k := range volatile {
		if _, exists := newAttrs[k]; !exists {
			continue
		}
		newTypes[k] = types.DynamicType
		newAttrs[k] = types.DynamicUnknown()
	}

	newObj, d := types.ObjectValue(newTypes, newAttrs)
	diags.Append(d...)
	if diags.HasError() {
		return types.DynamicUnknown(), diags
	}

	return types.DynamicValue(newObj), diags
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
	if !r.requireRunner(&resp.Diagnostics) {
		return
	}

	ctx, cancel := withOpTimeout(ctx, plan.Timeouts.Create, defaultCreateTimeout, &resp.Diagnostics)
	defer cancel()
	if resp.Diagnostics.HasError() {
		return
	}

	typeName := plan.Type.ValueString()
	vars := mergeVars(ctx, plan.Vars, config.Sensitive)

	tflog.Debug(ctx, "Seristack Create: running stack",
		map[string]any{"type": typeName, "stack": "CREATE"})

	earlyID := ""
	result, err := runOperation(ctx, r.runner, typeName, "CREATE", vars,
		func(ctx context.Context, id string) error {

			early := *plan
			early.ID = types.StringValue(id)
			early.Output = types.DynamicNull()
			early.Sensitive = types.MapNull(types.StringType)
			d := resp.State.Set(ctx, &early)
			resp.Diagnostics.Append(d...)
			if d.HasError() {
				return errors.New("could not save the early state")
			}
			earlyID = id
			return nil
		})
	if err != nil {
		detail := err.Error()
		if earlyID != "" {
			detail += taintNote
		}
		resp.Diagnostics.AddError("Seristack Create Error", detail)
		return
	}
	if !result.Success {
		detail := fmt.Sprintf("stack: %q\nerror: %s\noutput: %s", result.Name, result.Error, result.Output)
		if earlyID != "" {
			detail += taintNote
		}
		resp.Diagnostics.AddError("Seristack Create Execution Failed", detail)
		return
	}

	id := extractIDFromOutput(result.Output)
	if id == "" {
		detail := fmt.Sprintf("Create stack %q must return JSON with an 'id' field. Got: %s", result.Name, result.Output)
		if earlyID != "" {
			detail += taintNote
		}
		resp.Diagnostics.AddError("Seristack Create Error", detail)
		return
	}
	if earlyID != "" && id != earlyID {
		resp.Diagnostics.AddError("Seristack Create Error",
			fmt.Sprintf("The id changed from %q (pending) to %q (succeeded); it must stay the same.", earlyID, id)+taintNote)
		return
	}

	tflog.Debug(ctx, "Seristack Create: success", map[string]any{"id": id})

	plan.ID = types.StringValue(id)

	output, diags := extractOutputFromOutput(result.Output)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	plan.Output = output
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *SeristackResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	state, ok := extractModel[SeristackResourceModel](ctx, req.State.Get, &resp.Diagnostics)
	if !ok {
		return
	}
	if !r.requireRunner(&resp.Diagnostics) {
		return
	}

	ctx, cancel := withOpTimeout(ctx, state.Timeouts.Read, defaultReadTimeout, &resp.Diagnostics)
	defer cancel()
	if resp.Diagnostics.HasError() {
		return
	}

	typeName := state.Type.ValueString()

	vars := flattenVars(state.Vars)
	vars["id"] = state.ID.ValueString()
	if err := r.addOutputVar(vars, state.Output); err != nil {
		resp.Diagnostics.AddError("Output Serialization Error", err.Error())
		return
	}

	tflog.Debug(ctx, "Seristack Read: running stack",
		map[string]any{"type": typeName, "stack": "READ", "id": state.ID.ValueString()})

	result, err := r.runner.Run(ctx, typeName, "READ", vars)
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

	output, diags := extractOutputFromOutput(result.Output)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	state.Output = output

	refreshedVars, foundVars, diags := extractVarsFromOutput(result.Output)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if foundVars && len(refreshedVars) > 0 {
		mergedVars := flattenVars(state.Vars)
		for key, value := range refreshedVars {
			mergedVars[key] = value
		}

		varsValue, mapDiags := types.MapValueFrom(ctx, types.StringType, mergedVars)
		resp.Diagnostics.Append(mapDiags...)
		if resp.Diagnostics.HasError() {
			return
		}
		state.Vars = varsValue
	}

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
	if !r.requireRunner(&resp.Diagnostics) {
		return
	}

	typeName := plan.Type.ValueString()

	if state.Vars.Equal(plan.Vars) {
		tflog.Info(ctx, "Seristack Update: vars unchanged, skipping execution")
		plan.ID = state.ID
		plan.Output = state.Output
		resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
		return
	}

	ctx, cancel := withOpTimeout(ctx, plan.Timeouts.Update, defaultUpdateTimeout, &resp.Diagnostics)
	defer cancel()
	if resp.Diagnostics.HasError() {
		return
	}

	vars := mergeVars(ctx, plan.Vars, config.Sensitive)
	vars["id"] = state.ID.ValueString()
	if err := r.addOutputVar(vars, state.Output); err != nil {
		resp.Diagnostics.AddError("Output Serialization Error", err.Error())
		return
	}

	tflog.Debug(ctx, "Seristack Update: running stack",
		map[string]any{"type": typeName, "stack": "UPDATE", "id": state.ID.ValueString()})

	result, err := runOperation(ctx, r.runner, typeName, "UPDATE", vars, nil)
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
		resp.Diagnostics.AddWarning("Seristack Update: id change ignored",
			fmt.Sprintf("Update stack returned id %q but the resource id is %q. "+
				"The id cannot change in place; add the var that determines it to the RECREATE_ON stack.",
				newID, state.ID.ValueString()))
	}
	plan.ID = state.ID

	output, diags := extractOutputFromOutput(result.Output)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	plan.Output = output
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *SeristackResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	state, ok := extractModel[SeristackResourceModel](ctx, req.State.Get, &resp.Diagnostics)
	if !ok {
		return
	}
	if !r.requireRunner(&resp.Diagnostics) {
		return
	}

	ctx, cancel := withOpTimeout(ctx, state.Timeouts.Delete, defaultDeleteTimeout, &resp.Diagnostics)
	defer cancel()
	if resp.Diagnostics.HasError() {
		return
	}

	typeName := state.Type.ValueString()

	vars := flattenVars(state.Vars)
	vars["id"] = state.ID.ValueString()
	if err := r.addOutputVar(vars, state.Output); err != nil {
		resp.Diagnostics.AddError("Output Serialization Error", err.Error())
		return
	}

	tflog.Debug(ctx, "Seristack Delete: running stack",
		map[string]any{"type": typeName, "stack": "DELETE", "id": state.ID.ValueString()})

	result, err := runOperation(ctx, r.runner, typeName, "DELETE", vars, nil)
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
	if !r.requireRunner(&resp.Diagnostics) {
		return
	}

	readVars := make(map[string]string, len(imp.Vars)+1)
	for k, v := range imp.Vars {
		readVars[k] = v
	}
	readVars["id"] = imp.ID

	result, err := r.runner.Run(ctx, imp.Type, "READ", readVars)
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

	output, diags := extractOutputFromOutput(result.Output)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	data := SeristackResourceModel{
		ID:        types.StringValue(imp.ID),
		Type:      types.StringValue(imp.Type),
		Vars:      varsValue,
		Sensitive: types.MapNull(types.StringType),
		Output:    output,
		Timeouts:  nullTimeouts(),
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *SeristackResource) requireRunner(diags *diag.Diagnostics) bool {
	if r.runner == nil {
		diags.AddError("Opsy Provider Not Configured",
			"The provider needs a `local` or `http` block to run seristack stacks.")
		return false
	}
	return true
}

func (r *SeristackResource) addOutputVar(vars map[string]string, out types.Dynamic) error {
	if _, isHTTP := r.runner.(*httpRunner); isHTTP {
		return nil
	}
	outputJSON, err := dynamicValueToJSON(out)
	if err != nil {
		return err
	}
	vars["_output"] = outputJSON
	return nil
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
