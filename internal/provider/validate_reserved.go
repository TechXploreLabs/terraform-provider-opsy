package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const reservedVarPrefix = "seristack_"

func (r *SeristackResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	for _, attrName := range []string{"vars", "sensitive"} {
		var m types.Map
		resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root(attrName), &m)...)
		if resp.Diagnostics.HasError() {
			return
		}

		if m.IsNull() || m.IsUnknown() {
			continue
		}
		for key := range m.Elements() {
			if strings.HasPrefix(strings.ToLower(key), reservedVarPrefix) {
				resp.Diagnostics.AddAttributeError(
					path.Root(attrName).AtMapKey(key),
					"Reserved variable name",
					fmt.Sprintf("The variable name %q is reserved: names starting with %q are set by the "+
						"provider (for example seristack_status, seristack_status_vars). Rename it.",
						key, reservedVarPrefix),
				)
			}
		}
	}
}
