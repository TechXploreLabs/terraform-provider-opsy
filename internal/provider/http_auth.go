// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	opsyseristackaction "github.com/TechXploreLabs/seristack/pkg/opsy"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

const (
	// If the listed key is "access_token", the value is sent as
	// "Authorization: Bearer <value>". Any other listed key is sent as a
	// header with that exact name and the value as is.
	accessTokenKey = "access_token"

	// Optional key in the stack's JSON (not listed in `output`): lifetime in
	// seconds. Used only to know when to run the stack again.
	expiresInKey = "expires_in"

	expirySkew = time.Minute
)

// authSource runs the provider stack on this machine, keeps the resulting
// headers in memory, and runs the stack again when they expire or the server
// answers 401. It is safe for concurrent use.
type authSource struct {
	base      *opsyseristackaction.Config // holds the parsed YAML in .Config
	stackName string
	vars      map[string]string
	keys      []string

	mu      sync.Mutex
	headers map[string]string
	expires time.Time // zero = no expiry known
	gen     uint64    // increments on every refresh
}

// Headers returns the current headers and a generation number that must be
// passed to Invalidate if the server rejects them.
func (a *authSource) Headers(ctx context.Context) (map[string]string, uint64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.headers == nil || (!a.expires.IsZero() && !time.Now().Before(a.expires)) {
		if err := a.refreshLocked(ctx); err != nil {
			return nil, 0, err
		}
	}
	return copyHeaders(a.headers), a.gen, nil
}

// Invalidate drops the cached headers, but only if they are still the
// generation the caller used. This stops a burst of parallel 401s from
// running the stack once per request.
func (a *authSource) Invalidate(gen uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if gen == a.gen {
		a.headers = nil
	}
}

func (a *authSource) refreshLocked(ctx context.Context) error {
	tflog.Debug(ctx, "Seristack HTTP: running provider stack for credentials",
		map[string]any{"stack": a.stackName})

	vars := make(map[string]string, len(a.vars))
	for k, v := range a.vars {
		vars[k] = v
	}

	result, err := opsyseristackaction.OpsySeristack(&opsyseristackaction.Config{
		Config:    a.base.Config,
		StackName: a.stackName,
		Vars:      vars,
		Format:    "json",
	})
	if err != nil {
		return fmt.Errorf("provider stack %q: %w", a.stackName, err)
	}
	if !result.Success {
		return fmt.Errorf("provider stack %q failed: %s", a.stackName, truncate(result.Error, 500))
	}

	headers, expires, err := headersFromStackOutput(result.Output, a.keys)
	if err != nil {
		return fmt.Errorf("provider stack %q: %w", a.stackName, err)
	}

	a.headers = headers
	a.expires = expires
	a.gen++
	return nil
}

// headersFromStackOutput turns the provider stack's JSON output into request
// headers. The output itself is never put in an error message, because it
// contains credentials.
func headersFromStackOutput(output string, keys []string) (map[string]string, time.Time, error) {
	var m map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &m); err != nil {
		return nil, time.Time{}, fmt.Errorf(
			"the stack must print one JSON object (its output is not shown because it may contain credentials)")
	}

	headers := make(map[string]string, len(keys))
	for _, key := range keys {
		raw, ok := m[key]
		if !ok {
			present := make([]string, 0, len(m))
			for k := range m {
				present = append(present, k)
			}
			sort.Strings(present)
			return nil, time.Time{}, fmt.Errorf(
				"key %q listed in `output` is not in the stack's JSON (keys present: %s)",
				key, strings.Join(present, ", "))
		}

		value, ok := raw.(string)
		if !ok || value == "" {
			return nil, time.Time{}, fmt.Errorf("key %q must be a non-empty JSON string", key)
		}
		if strings.ContainsAny(value, "\r\n") {
			return nil, time.Time{}, fmt.Errorf("the value of key %q contains a line break", key)
		}

		name := key
		if key == accessTokenKey {
			name = "Authorization"
			value = "Bearer " + value
		}
		if name == "" || strings.ContainsAny(name, " \t\r\n:") {
			return nil, time.Time{}, fmt.Errorf("key %q is not a valid HTTP header name", key)
		}
		headers[name] = value
	}

	var expires time.Time
	if raw, ok := m[expiresInKey]; ok {
		if secs := toSeconds(raw); secs > 0 {
			d := time.Duration(secs * float64(time.Second))
			if d > 2*expirySkew {
				d -= expirySkew
			} else {
				d /= 2
			}
			expires = time.Now().Add(d)
		}
	}

	return headers, expires, nil
}

func toSeconds(raw any) float64 {
	switch v := raw.(type) {
	case float64:
		return v
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			return f
		}
	}
	return 0
}

func copyHeaders(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// buildAuthSource reads the provider-stack settings of the http block.
//
//   - none of provider_stack_file / provider_stack_name / output set:
//     no credentials are sent (fine for a local dev server).
//   - otherwise all three are required, and the YAML is parsed now so a bad
//     file fails at configure time. The stack itself runs lazily, on the
//     first request.
//
// It also returns the known `vars`, which hold the reserved keys the
// provider reads itself (ca_cert_file, timeout).
func buildAuthSource(ctx context.Context, m *HttpModel) (*authSource, map[string]string, diag.Diagnostics) {
	var diags diag.Diagnostics

	vars, d := knownStringMap(m.Vars)
	diags.Append(d...)
	if diags.HasError() {
		return nil, nil, diags
	}

	if m.ProviderStackFile.IsUnknown() || m.ProviderStackName.IsUnknown() || m.Output.IsUnknown() {
		diags.AddError("Unknown Provider Configuration",
			"`provider_stack_file`, `provider_stack_name` and `output` must be known at configure time.")
		return nil, nil, diags
	}

	file := m.ProviderStackFile.ValueString()
	stackName := m.ProviderStackName.ValueString()

	var keys []string
	if !m.Output.IsNull() {
		diags.Append(m.Output.ElementsAs(ctx, &keys, false)...)
		if diags.HasError() {
			return nil, nil, diags
		}
	}

	if file == "" && stackName == "" && len(keys) == 0 {
		return nil, vars, diags
	}

	if file == "" || stackName == "" || len(keys) == 0 {
		diags.AddError("Invalid Provider Configuration",
			"To authenticate, the `http` block needs all of `provider_stack_file`, "+
				"`provider_stack_name` and `output`. Leave all three out to send no credentials.")
		return nil, nil, diags
	}

	content, err := os.ReadFile(file)
	if err != nil {
		diags.AddError("Invalid Provider Configuration",
			fmt.Sprintf("Cannot read provider_stack_file %q: %v", file, err))
		return nil, nil, diags
	}

	parsed, err := opsyseristackaction.NewConfigFromYAML(content)
	if err != nil {
		diags.AddError("Invalid Provider Configuration",
			fmt.Sprintf("Cannot parse provider_stack_file %q: %v", file, err))
		return nil, nil, diags
	}

	return &authSource{
		base:      &opsyseristackaction.Config{Config: parsed},
		stackName: stackName,
		vars:      vars,
		keys:      keys,
	}, vars, diags
}

// knownStringMap converts a map that must be fully known at configure time.
func knownStringMap(m types.Map) (map[string]string, diag.Diagnostics) {
	var diags diag.Diagnostics
	out := map[string]string{}

	if m.IsNull() {
		return out, diags
	}
	if m.IsUnknown() {
		diags.AddError("Unknown Provider Configuration",
			"The `http` block `vars` must be known at configure time.")
		return nil, diags
	}

	for k, v := range m.Elements() {
		sv, ok := v.(types.String)
		if !ok || sv.IsUnknown() {
			diags.AddError("Unknown Provider Configuration",
				fmt.Sprintf("The `http` block vars[%q] must be a known string at configure time.", k))
			continue
		}
		if sv.IsNull() {
			continue
		}
		out[k] = sv.ValueString()
	}
	return out, diags
}
