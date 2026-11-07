// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	opsyseristackaction "github.com/TechXploreLabs/seristack/pkg/opsy"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// ---------------------------------------------------------------------------
// StackRunner: the one abstraction the resource, data source, ephemeral
// resource and action use to execute a stack.
// ---------------------------------------------------------------------------

// StackRunner executes the stack `stack` (CREATE, READ, UPDATE, DELETE,
// DATASOURCE, RECREATE_ON, OUTPUT_CHANGES_ON, OPEN) of resource type
// `typeName` and returns the same Result type the seristack library uses.
type StackRunner interface {
	Run(ctx context.Context, typeName, stack string, vars map[string]string) (opsyseristackaction.Result, error)
}

// stackHTTPMethods maps a stack to the HTTP method registered on the server.
// It must match the `method:` values in the server YAML.
var stackHTTPMethods = map[string]string{
	"DATASOURCE":        http.MethodGet,
	"CREATE":            http.MethodPost,
	"READ":              http.MethodGet,
	"UPDATE":            http.MethodPut,
	"DELETE":            http.MethodDelete,
	"RECREATE_ON":       http.MethodGet,
	"OUTPUT_CHANGES_ON": http.MethodGet,
	"OPEN":              http.MethodPost,
}

// ---------------------------------------------------------------------------
// Local runner: runs stacks in-process from per-type YAML files.
// ---------------------------------------------------------------------------

type localRunner struct {
	scripts map[string]*opsyseristackaction.Config
}

func (l *localRunner) Run(_ context.Context, typeName, stack string, vars map[string]string) (opsyseristackaction.Result, error) {
	def, err := resolveScriptType(l.scripts, typeName)
	if err != nil {
		return opsyseristackaction.Result{}, err
	}
	return opsyseristackaction.OpsySeristack(&opsyseristackaction.Config{
		Config:    def.Config,
		StackName: stack,
		Vars:      vars,
		Format:    "json",
	})
}

func resolveScriptType(scripts map[string]*opsyseristackaction.Config, typeName string) (*opsyseristackaction.Config, error) {
	if scripts == nil {
		return nil, fmt.Errorf("provider scripts not initialised — check provider configuration")
	}
	def, ok := scripts[typeName]
	if !ok {
		keys := make([]string, 0, len(scripts))
		for k := range scripts {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return nil, fmt.Errorf("type %q not found in scripts bundle — available: %v", typeName, keys)
	}
	return def, nil
}

// ---------------------------------------------------------------------------
// HTTP runner: calls  <METHOD> <url>/<type>_<STACK>?key=value&...
// Credentials come from the provider stack (see http_auth.go).
// ---------------------------------------------------------------------------

const (
	maxResponseBytes = 16 << 20 // 16 MiB
	defaultTimeout   = 10 * time.Minute
)

// HttpModel is the provider `http` block.
//
// Only `url` is connection info. Everything about authentication lives in a
// local "provider stack": a stack in a local YAML file that is run on this
// machine and prints JSON. `output` lists the JSON keys that are sent on
// every request to the server.
type HttpModel struct {
	URL               types.String `tfsdk:"url"`
	ProviderStackFile types.String `tfsdk:"provider_stack_file"`
	ProviderStackName types.String `tfsdk:"provider_stack_name"`
	Vars              types.Map    `tfsdk:"vars"`
	Output            types.List   `tfsdk:"output"`
}

type httpRunner struct {
	baseURL string // no trailing slash
	client  *http.Client
	auth    *authSource // nil = no credentials
}

func newHTTPRunner(rawURL string, auth *authSource, caFile string, timeout time.Duration) (*httpRunner, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, fmt.Errorf("invalid url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("url scheme must be http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("url has no host")
	}
	u.RawQuery = ""
	u.Fragment = ""

	transport := http.DefaultTransport.(*http.Transport).Clone()
	if caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("cannot read ca_cert_file %q: %w", caFile, err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("ca_cert_file %q contains no valid PEM certificates", caFile)
		}
		transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	}

	return &httpRunner{
		baseURL: strings.TrimRight(u.String(), "/"),
		auth:    auth,
		client: &http.Client{
			Timeout:   timeout,
			Transport: transport,
			// Never follow redirects: an auth proxy answers bad credentials
			// with a redirect to a login page, which must be reported as an
			// auth failure, not followed.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

// newHTTPRunnerFromModel validates the http block and builds the runner.
// Reserved keys in `vars` that the provider itself consumes (they are still
// passed to the provider stack too):
//
//	ca_cert_file  path to a PEM bundle added to the trusted roots
//	timeout       Go duration for each request, default 10m
func newHTTPRunnerFromModel(ctx context.Context, m *HttpModel) (StackRunner, diag.Diagnostics) {
	var diags diag.Diagnostics

	if m.URL.IsUnknown() || m.URL.IsNull() || m.URL.ValueString() == "" {
		diags.AddError("Invalid Provider Configuration",
			"The `http` block requires a known, non-empty `url`.")
		return nil, diags
	}

	auth, vars, d := buildAuthSource(ctx, m)
	diags.Append(d...)
	if diags.HasError() {
		return nil, diags
	}

	timeout := defaultTimeout
	if s := vars["timeout"]; s != "" {
		parsed, err := time.ParseDuration(s)
		if err != nil || parsed <= 0 {
			diags.AddError("Invalid Provider Configuration",
				fmt.Sprintf("http vars[\"timeout\"] must be a positive duration such as \"10m\", got %q.", s))
			return nil, diags
		}
		timeout = parsed
	}

	runner, err := newHTTPRunner(m.URL.ValueString(), auth, vars["ca_cert_file"], timeout)
	if err != nil {
		diags.AddError("Invalid Provider Configuration", err.Error())
		return nil, diags
	}

	if auth != nil {
		if u, err := url.Parse(m.URL.ValueString()); err == nil && u.Scheme == "http" && !isLoopbackHost(u.Hostname()) {
			diags.AddWarning("Credentials sent over plain HTTP",
				"The `http` url uses http:// for a non-local host, so credentials and variables travel unencrypted. Use https://.")
		}
	}

	return runner, diags
}

func isLoopbackHost(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

type httpReply struct {
	status   int
	body     []byte
	location string
}

func (h *httpRunner) Run(ctx context.Context, typeName, stack string, vars map[string]string) (opsyseristackaction.Result, error) {
	method, ok := stackHTTPMethods[stack]
	if !ok {
		// Custom stack names (used by the action) can change things, so they
		// are sent as POST. The stack on the server must declare `method: POST`.
		method = http.MethodPost
	}

	name := typeName + "_" + stack
	endpoint := h.baseURL + "/" + url.PathEscape(name) // safe to show in errors: no query

	q := url.Values{}
	for k, v := range vars {
		q.Set(k, v)
	}

	u, err := url.Parse(endpoint)
	if err != nil {
		return opsyseristackaction.Result{}, fmt.Errorf("invalid endpoint for stack %q", name)
	}
	u.RawQuery = q.Encode()
	target := u.String()

	tflog.Debug(ctx, "Seristack HTTP: calling stack",
		map[string]any{"method": method, "endpoint": endpoint, "var_count": len(vars)})

	for attempt := 0; ; attempt++ {
		reply, gen, err := h.do(ctx, method, target, endpoint)
		if err != nil {
			return opsyseristackaction.Result{}, err
		}

		// Expired or revoked credentials: run the provider stack again once.
		if reply.status == http.StatusUnauthorized && h.auth != nil && attempt == 0 {
			h.auth.Invalidate(gen)
			continue
		}

		switch {
		case reply.status >= 300 && reply.status < 400:
			return opsyseristackaction.Result{
				Name:    name,
				Success: false,
				Error:   redirectMessage(endpoint, reply.status, reply.location),
			}, nil
		case reply.status == http.StatusUnauthorized || reply.status == http.StatusForbidden:
			return opsyseristackaction.Result{
				Name:    name,
				Success: false,
				Error: fmt.Sprintf("HTTP %d from %s: the server rejected the credentials. %s",
					reply.status, endpoint, truncate(strings.TrimSpace(string(reply.body)), 500)),
			}, nil
		}

		return decodeStackResponse(name, reply.status, reply.body), nil
	}
}

func (h *httpRunner) do(ctx context.Context, method, target, endpoint string) (httpReply, uint64, error) {
	req, err := http.NewRequestWithContext(ctx, method, target, nil)
	if err != nil {
		return httpReply{}, 0, fmt.Errorf("could not build %s request for %s", method, endpoint)
	}
	req.Header.Set("Accept", "application/json")

	var gen uint64
	if h.auth != nil {
		headers, g, err := h.auth.Headers(ctx)
		if err != nil {
			return httpReply{}, 0, err
		}
		gen = g
		for k, v := range headers {
			req.Header.Set(k, v)
		}
	}

	resp, err := h.client.Do(req)
	if err != nil {
		// *url.Error contains the full URL, including the query string (vars,
		// possibly secrets). Unwrap it so only the cause is shown.
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err
		}
		return httpReply{}, 0, fmt.Errorf("%s %s failed: %w", method, endpoint, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return httpReply{}, 0, fmt.Errorf("reading response from %s: %w", endpoint, err)
	}

	return httpReply{
		status:   resp.StatusCode,
		body:     body,
		location: resp.Header.Get("Location"),
	}, gen, nil
}

// redirectMessage explains a redirect without printing the Location query
// string (login redirects usually carry the original URL, vars included).
func redirectMessage(endpoint string, status int, location string) string {
	shown := "(no Location header)"
	if loc, err := url.Parse(location); err == nil && loc.Host != "" {
		shown = loc.Scheme + "://" + loc.Host + loc.Path
	} else if location != "" {
		shown = "(relative redirect)"
	}
	return fmt.Sprintf("HTTP %d from %s redirecting to %s. An auth proxy usually does this when the "+
		"credentials are missing or rejected; check the provider stack output and the proxy's bearer-token settings.",
		status, endpoint, shown)
}

// decodeStackResponse converts an HTTP response into the Result the rest of
// the provider expects (Output = the stack's stdout JSON as text).
//
// Two response shapes are accepted, because the server contract is not
// pinned down yet:
//
//  1. Wrapper: {"success": true, "output": "<stack stdout>", "error": ""}
//     (detected by the presence of a top-level "success" key)
//  2. Raw: the body IS the stack's stdout, e.g. {"id": "...", "output": {...}}
func decodeStackResponse(name string, status int, body []byte) opsyseristackaction.Result {
	text := strings.TrimSpace(string(body))
	httpOK := status >= 200 && status <= 299

	res := opsyseristackaction.Result{Name: name}

	var env struct {
		Success *bool           `json:"success"`
		Output  json.RawMessage `json:"output"`
		Error   string          `json:"error"`
	}
	if err := json.Unmarshal([]byte(text), &env); err == nil && env.Success != nil {
		res.Success = *env.Success && httpOK
		res.Error = env.Error

		var asString string
		if err := json.Unmarshal(env.Output, &asString); err == nil {
			res.Output = strings.TrimSpace(asString)
		} else {
			res.Output = strings.TrimSpace(string(env.Output))
		}
	} else {
		res.Success = httpOK
		res.Output = text
	}

	if !res.Success && res.Error == "" {
		res.Error = fmt.Sprintf("HTTP %d: %s", status, truncate(text, 2000))
	}
	return res
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ---------------------------------------------------------------------------
// Local source loading (moved out of Configure so it can return diagnostics)
// ---------------------------------------------------------------------------

func loadLocalScripts(ctx context.Context, local *LocalModel) (map[string]*opsyseristackaction.Config, diag.Diagnostics) {
	var diags diag.Diagnostics

	if local.Path.IsUnknown() || local.Folder_path.IsUnknown() {
		diags.AddError("Unknown Provider Configuration",
			"`path` and `folder_path` must be known at configure time.")
		return nil, diags
	}

	hasFolder := !local.Folder_path.IsNull() && local.Folder_path.ValueString() != ""
	hasZip := !local.Path.IsNull() && local.Path.ValueString() != ""

	switch {
	case hasFolder && hasZip:
		diags.AddError("Invalid Provider Configuration",
			"The `local` block sets both `path` and `folder_path`; set exactly one.")
		return nil, diags
	case !hasFolder && !hasZip:
		diags.AddError("Invalid Provider Configuration",
			"The `local` block must set either `path` or `folder_path`.")
		return nil, diags
	}

	var (
		scripts    map[string]*opsyseristackaction.Config
		sourceUsed string
		err        error
	)

	if hasZip {
		zipPath := local.Path.ValueString()
		sourceUsed = "path:" + zipPath
		scripts, err = loadZipFile(zipPath)
	} else {
		dir := local.Folder_path.ValueString()
		sourceUsed = "folder_path:" + dir
		scripts, err = loadYAMLDirectory(dir)
	}

	if err != nil {
		diags.AddError("Opsy Provider Configuration Error",
			fmt.Sprintf("Failed to load scripts from %s: %v", sourceUsed, err))
		return nil, diags
	}

	tflog.Debug(ctx, "Opsy: scripts loaded",
		map[string]any{"source": sourceUsed, "types_loaded": len(scripts)})

	return scripts, diags
}
