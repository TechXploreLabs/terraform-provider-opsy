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
	scripts map[string]*opsyseristackaction.Config
}

type OpsyProviderModel struct {
	Local []LocalModel `tfsdk:"local"`
}

type LocalModel struct {
	Path types.String `tfsdk:"path"`
}

func (p *OpsyProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "opsy"
	resp.Version = p.version
}

func (p *OpsyProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "The **opsy** provider downloads a zip bundle of seristack YAML " +
			"definitions from a local file, holds them in memory for the duration " +
			"of the run, and never writes them to disk.",

		Blocks: map[string]schema.Block{

			"local": schema.ListNestedBlock{
				MarkdownDescription: "Local zip file path. Tried first before any remote source. Useful for development or air-gapped environments.",
				NestedObject: schema.NestedBlockObject{
					Attributes: map[string]schema.Attribute{
						"path": schema.StringAttribute{
							Required:            true,
							MarkdownDescription: "Absolute or relative path to a `.zip` file containing the seristack YAML definitions.",
						},
					},
				},
			},
		},
	}
}

func (p *OpsyProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var config OpsyProviderModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if len(config.Local) == 0 {
		resp.Diagnostics.AddError(
			"Invalid Provider Configuration",
			"One source must be configured: `local`.",
		)
		return
	}

	var zipBytes []byte
	var sourceUsed string

	for _, local := range config.Local {
		localPath := local.Path.ValueString()
		data, err := os.ReadFile(localPath)
		if err != nil {
			tflog.Warn(ctx, "Skipping local source: file read failed",
				map[string]any{"path": localPath, "error": err.Error()})
			continue
		}
		zipBytes = data
		sourceUsed = "local:" + localPath
		break
	}

	if zipBytes == nil {
		resp.Diagnostics.AddError(
			"Opsy Provider Configuration Error",
			"No scripts bundle could be loaded from configured local source. "+
				"Check your provider block and ensure the source is reachable.",
		)
		return
	}

	tflog.Debug(ctx, "Opsy: scripts bundle loaded", map[string]any{"source": sourceUsed})

	scripts, err := parseZipBundle(zipBytes)
	if err != nil {
		resp.Diagnostics.AddError("Opsy Scripts Parse Error",
			fmt.Sprintf("Failed to parse scripts bundle from %q: %v", sourceUsed, err))
		return
	}

	tflog.Debug(ctx, "Opsy: scripts bundle parsed",
		map[string]any{"source": sourceUsed, "types_loaded": len(scripts)})

	p.scripts = scripts
	resp.ResourceData = p
	resp.DataSourceData = p
	resp.ActionData = p
}

func (p *OpsyProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewSeristackResource,
	}
}

func (p *OpsyProvider) EphemeralResources(_ context.Context) []func() ephemeral.EphemeralResource {
	return []func() ephemeral.EphemeralResource{}
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

		name := normalizeEntryName(f.Name)
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
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

		typeName := strings.TrimSuffix(strings.TrimSuffix(name, ".yaml"), ".yml")
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
