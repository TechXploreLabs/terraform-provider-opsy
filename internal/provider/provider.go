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
	scripts map[string]*opsyseristackaction.Config
}

type OpsyProviderModel struct {
	Local *LocalModel `tfsdk:"local"`
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
		MarkdownDescription: "The **opsy** provider loads seristack YAML definitions from a local " +
			"directory or a local zip bundle, holds them in memory for the duration " +
			"of the run, and never writes them to disk.",

		Blocks: map[string]schema.Block{

			"local": schema.SingleNestedBlock{
				MarkdownDescription: "Local source of Seristack YAML definitions. " +
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

	if config.Local == nil {
		tflog.Debug(ctx, "Opsy: no local block configured; seristack scripts unavailable")
		resp.ResourceData = p
		resp.DataSourceData = p
		resp.ActionData = p
		return
	}

	local := config.Local

	if local.Path.IsUnknown() || local.Folder_path.IsUnknown() {
		resp.Diagnostics.AddError(
			"Unknown Provider Configuration",
			"`path` and `folder_path` must be known at configure time.",
		)
		return
	}

	hasFolder := !local.Folder_path.IsNull() && local.Folder_path.ValueString() != ""
	hasZip := !local.Path.IsNull() && local.Path.ValueString() != ""

	switch {
	case hasFolder && hasZip:
		resp.Diagnostics.AddError(
			"Invalid Provider Configuration",
			"The `local` block sets both `path` and `folder_path`; set exactly one.",
		)
		return
	case !hasFolder && !hasZip:
		resp.Diagnostics.AddError(
			"Invalid Provider Configuration",
			"The `local` block must set either `path` or `folder_apth`.",
		)
		return
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
		resp.Diagnostics.AddError(
			"Opsy Provider Configuration Error",
			fmt.Sprintf("Failed to load scripts from %s: %v", sourceUsed, err),
		)
		return
	}

	tflog.Debug(ctx, "Opsy: scripts loaded",
		map[string]any{
			"source":       sourceUsed,
			"types_loaded": len(scripts),
		})

	p.scripts = scripts
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
