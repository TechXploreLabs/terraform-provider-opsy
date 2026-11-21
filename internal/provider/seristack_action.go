// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/action"
	"github.com/hashicorp/terraform-plugin-framework/action/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ action.Action = &SeristackAction{}
var _ action.ActionWithConfigure = &SeristackAction{}

const defaultActionTimeout = 30 * time.Minute

func NewSeristackAction() action.Action {
	return &SeristackAction{}
}

type SeristackAction struct {
	runner StackRunner
}

type SeristackActionModel struct {
	Type    types.String `tfsdk:"type"`
	Vars    types.Map    `tfsdk:"vars"`
	Timeout types.String `tfsdk:"timeout"`
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
			"vars": schema.MapAttribute{
				MarkdownDescription: "Variables to pass to the stack. Names starting with `seristack_` are reserved.",
				Optional:            true,
				ElementType:         types.StringType,
			},
			"timeout": schema.StringAttribute{
				MarkdownDescription: "Go duration limiting the whole run, status checks included, for example `45m`. Default `30m`. " +
					"If the stack answers `seristack_status = pending`, the provider calls it again after `seristack_retry` " +
					"until it reports `succeeded` or `failed`.",
				Optional: true,
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

	resp.SendProgress(action.InvokeProgressEvent{
		Message: "starting action invocation",
	})

	var data SeristackActionModel

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

	for k := range vars {
		if strings.HasPrefix(strings.ToLower(k), reservedVarPrefix) {
			resp.Diagnostics.AddAttributeError(
				path.Root("vars").AtMapKey(k),
				"Reserved variable name",
				fmt.Sprintf("The variable name %q is reserved: names starting with %q are set by the provider.",
					k, reservedVarPrefix),
			)
		}
	}
	if resp.Diagnostics.HasError() {
		return
	}

	typeName := data.Type.ValueString()

	timeout := defaultActionTimeout
	if !data.Timeout.IsNull() && !data.Timeout.IsUnknown() && data.Timeout.ValueString() != "" {
		d, perr := time.ParseDuration(data.Timeout.ValueString())
		if perr != nil || d <= 0 {
			resp.Diagnostics.AddAttributeError(path.Root("timeout"), "Invalid timeout",
				fmt.Sprintf("`timeout` must be a positive duration such as \"45m\", got %q.", data.Timeout.ValueString()))
			return
		}
		timeout = d
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()

	result, err := runOperationProgress(runCtx, e.runner, typeName, "ACTION", vars,
		func(_ context.Context, id string) error {
			resp.SendProgress(action.InvokeProgressEvent{
				Message: fmt.Sprintf("stack '%s' accepted the request (id=%s)", "ACTION", id),
			})
			return nil
		},
		func(wait time.Duration) {
			resp.SendProgress(action.InvokeProgressEvent{
				Message: fmt.Sprintf("stack '%s' is pending; checking again in %s", "ACTION", wait.Round(time.Second)),
			})
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
		Message: fmt.Sprintf("stack '%s' completed in %s", result.Name, time.Since(start).Round(time.Millisecond)),
	})
}
