// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	opsyseristackaction "github.com/TechXploreLabs/seristack/pkg/opsy"
	"github.com/hashicorp/terraform-plugin-framework/action"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

var _ provider.Provider = &OpsyProvider{}
var _ provider.ProviderWithFunctions = &OpsyProvider{}

type OpsyProvider struct {
	version string

	// scripts is set only in local mode (kept for code that still reads it).
	scripts map[string]*opsyseristackaction.Config

	// runner executes stacks: localRunner or httpRunner. nil when the
	// provider block has neither a local nor an http block (functions only).
	runner StackRunner
}

type OpsyProviderModel struct {
	Local *LocalModel `tfsdk:"local"`
	Http  *HttpModel  `tfsdk:"http"`
}

type LocalModel struct {
	Path        types.String `tfsdk:"path"`
	Folder_path types.String `tfsdk:"folder_path"`
}

func (p *OpsyProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "opsy"
	resp.Version = p.version
}

func (p *OpsyProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "The **opsy** provider runs seristack stacks, either locally from YAML " +
			"definitions or on a seristack HTTP server. Set at most one of the `local` and `http` blocks.",

		Blocks: map[string]schema.Block{

			"local": schema.SingleNestedBlock{
				MarkdownDescription: "Run stacks locally from YAML definitions. " +
					"Set exactly one of `path` or `folder_path`.",
				Attributes: map[string]schema.Attribute{
					"path": schema.StringAttribute{
						Optional: true,
						MarkdownDescription: "Absolute or relative path to a `.zip` file " +
							"containing the seristack YAML definitions. Conflicts with `folder_path`.",
					},
					"folder_path": schema.StringAttribute{
						Optional: true,
						MarkdownDescription: "Absolute or relative path to a directory " +
							"containing `.yaml` or `.yml` files. Conflicts with `path`.",
					},
				},
			},

			"http": schema.SingleNestedBlock{
				MarkdownDescription: "Run stacks on a seristack HTTP server. Each stack is called as " +
					"`<url>/<type>_<STACK>` with the variables sent as query parameters. " +
					"Authentication is done by a local *provider stack*: a stack in a local YAML file that " +
					"prints JSON. The keys listed in `output` are sent on every request.",
				Attributes: map[string]schema.Attribute{
					"url": schema.StringAttribute{
						Optional: true,
						MarkdownDescription: "Base URL of the seristack server, for example " +
							"`https://seristack.example.com/`. Required when the `http` block is present.",
					},
					"provider_stack_file": schema.StringAttribute{
						Optional: true,
						MarkdownDescription: "Path to a local YAML file that contains the provider stack. " +
							"Required together with `provider_stack_name` and `output`.",
					},
					"provider_stack_name": schema.StringAttribute{
						Optional: true,
						MarkdownDescription: "Name of the stack in `provider_stack_file`. It runs on this machine " +
							"(not on the server) the first time credentials are needed, and again when they " +
							"expire or the server answers 401.",
					},
					"vars": schema.MapAttribute{
						Optional:    true,
						Sensitive:   true,
						ElementType: types.StringType,
						MarkdownDescription: "Variables passed to the provider stack (for example `token_url`, " +
							"`client_id`, `client_secret`, `audience`). Declare each one under `vars:` in the stack. " +
							"Two keys are also read by the provider itself: `ca_cert_file` (PEM bundle to trust) " +
							"and `timeout` (per-request Go duration, default `10m`).",
					},
					"output": schema.ListAttribute{
						Optional:    true,
						ElementType: types.StringType,
						MarkdownDescription: "Keys of the provider stack's JSON output to send on every request. " +
							"The key `access_token` is sent as `Authorization: Bearer <value>`; any other key is " +
							"sent as an HTTP header with that name. An optional `expires_in` key (seconds) in the " +
							"JSON tells the provider when to run the stack again.",
					},
				},
			},
		},
	}
}

func (p *OpsyProvider) Configure(
	ctx context.Context,
	req provider.ConfigureRequest,
	resp *provider.ConfigureResponse,
) {
	var config OpsyProviderModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if config.Local != nil && config.Http != nil {
		resp.Diagnostics.AddError(
			"Invalid Provider Configuration",
			"Set either the `local` block or the `http` block, not both.",
		)
		return
	}

	switch {
	case config.Http != nil:
		runner, diags := newHTTPRunnerFromModel(ctx, config.Http)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		p.runner = runner
		tflog.Debug(ctx, "Opsy: using http source")

	case config.Local != nil:
		scripts, diags := loadLocalScripts(ctx, config.Local)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		p.scripts = scripts
		p.runner = &localRunner{scripts: scripts}

	default:
		// No source: only provider-defined functions are usable.
		tflog.Debug(ctx, "Opsy: no local or http block configured; seristack stacks unavailable")
	}

	resp.ResourceData = p
	resp.DataSourceData = p
	resp.ActionData = p
	resp.EphemeralResourceData = p
}

func (p *OpsyProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewSeristackResource,
	}
}

func (p *OpsyProvider) EphemeralResources(_ context.Context) []func() ephemeral.EphemeralResource {
	return []func() ephemeral.EphemeralResource{
		NewSeristackEphemeral,
	}
}

func (p *OpsyProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewSeristackDataSource,
	}
}

func (p *OpsyProvider) Functions(_ context.Context) []func() function.Function {
	return []func() function.Function{
		NewTimeCheckFunction,
		NewGetEnvVarFunction,
		NewOCIFunction,
	}
}

func (p *OpsyProvider) Actions(_ context.Context) []func() action.Action {
	return []func() action.Action{
		NewSeristackAction,
	}
}

func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &OpsyProvider{version: version}
	}
}

func loadYAMLDirectory(dir string) (map[string]*opsyseristackaction.Config, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to read YAML directory %q: %w", dir, err)
	}

	scripts := make(map[string]*opsyseristackaction.Config)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		ext := strings.ToLower(filepath.Ext(name))

		if ext != ".yaml" && ext != ".yml" {
			continue
		}

		filePath := filepath.Join(dir, name)

		content, err := os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("failed to read %s: %w", filePath, err)
		}

		parsed, err := opsyseristackaction.NewConfigFromYAML(content)
		if err != nil {
			return nil, fmt.Errorf("failed to parse YAML file %s: %w", filePath, err)
		}

		typeName := strings.TrimSuffix(name, filepath.Ext(name))

		if _, exists := scripts[typeName]; exists {
			return nil, fmt.Errorf("duplicate script type %q", typeName)
		}

		scripts[typeName] = &opsyseristackaction.Config{
			Config: parsed,
		}
	}

	if len(scripts) == 0 {
		return nil, fmt.Errorf("directory %q contains no valid .yaml or .yml files", dir)
	}

	return scripts, nil
}

func loadZipFile(zipPath string) (map[string]*opsyseristackaction.Config, error) {
	data, err := os.ReadFile(zipPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read zip file %q: %w", zipPath, err)
	}
	return parseZipBundle(data)
}

func parseZipBundle(data []byte) (map[string]*opsyseristackaction.Config, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("invalid zip bundle: %w", err)
	}

	scripts := make(map[string]*opsyseristackaction.Config)

	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}

		if strings.HasPrefix(f.Name, "__MACOSX/") {
			continue
		}

		name := normalizeEntryName(f.Name)
		if strings.HasPrefix(name, ".") {
			continue
		}

		lower := strings.ToLower(name)
		if !strings.HasSuffix(lower, ".yaml") && !strings.HasSuffix(lower, ".yml") {
			continue
		}

		content, err := readZipEntry(f)
		if err != nil {
			return nil, err
		}

		parsed, err := opsyseristackaction.NewConfigFromYAML(content)
		if err != nil {
			return nil, fmt.Errorf("failed to build config for %s: %w", f.Name, err)
		}

		typeName := strings.TrimSuffix(name, path.Ext(name))

		if _, exists := scripts[typeName]; exists {
			return nil, fmt.Errorf("duplicate script type %q in bundle", typeName)
		}

		scripts[typeName] = &opsyseristackaction.Config{
			Config: parsed,
		}
	}

	if len(scripts) == 0 {
		return nil, fmt.Errorf("scripts bundle contained no valid YAML files")
	}
	return scripts, nil
}

func normalizeEntryName(name string) string {
	if idx := strings.Index(name, "/"); idx != -1 {
		name = name[idx+1:]
	}
	return path.Base(name)
}

func readZipEntry(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("failed to open %s in bundle: %w", f.Name, err)
	}
	defer rc.Close()
	content, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s in bundle: %w", f.Name, err)
	}
	return content, nil
}
