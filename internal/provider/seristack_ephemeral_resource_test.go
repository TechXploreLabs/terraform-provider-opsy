package provider

import (
	"context"
	"testing"

	ephemeralfw "github.com/hashicorp/terraform-plugin-framework/ephemeral"
)

func TestSeristackEphemeralSchema(t *testing.T) {
	t.Parallel()

	e, ok := NewSeristackEphemeral().(*SeristackEphemeral)
	if !ok {
		t.Fatalf("expected *SeristackEphemeral")
	}

	var resp ephemeralfw.SchemaResponse
	e.Schema(context.Background(), ephemeralfw.SchemaRequest{}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("schema returned errors: %v", resp.Diagnostics)
	}

	for _, name := range []string{"type", "vars", "sensitive", "output"} {
		if _, ok := resp.Schema.Attributes[name]; !ok {
			t.Fatalf("ephemeral schema missing %q attribute", name)
		}
	}

	if diags := resp.Schema.ValidateImplementation(context.Background()); diags.HasError() {
		t.Fatalf("schema failed implementation validation: %v", diags)
	}
}

func TestSeristackEphemeralSchemaAttributeFlags(t *testing.T) {
	t.Parallel()

	e := NewSeristackEphemeral()

	var resp ephemeralfw.SchemaResponse
	e.Schema(context.Background(), ephemeralfw.SchemaRequest{}, &resp)

	if !resp.Schema.Attributes["type"].IsRequired() {
		t.Errorf("'type' must be required")
	}
	if !resp.Schema.Attributes["vars"].IsOptional() {
		t.Errorf("'vars' must be optional")
	}
	if !resp.Schema.Attributes["sensitive"].IsOptional() || !resp.Schema.Attributes["sensitive"].IsSensitive() {
		t.Errorf("'sensitive' must be optional and sensitive")
	}
	if !resp.Schema.Attributes["output"].IsComputed() || !resp.Schema.Attributes["output"].IsSensitive() {
		t.Errorf("'output' must be computed and sensitive")
	}
}

func TestSeristackEphemeralMetadata(t *testing.T) {
	t.Parallel()

	e := NewSeristackEphemeral()

	var resp ephemeralfw.MetadataResponse
	e.Metadata(context.Background(), ephemeralfw.MetadataRequest{ProviderTypeName: "seristack"}, &resp)

	if want := "seristack_seristack"; resp.TypeName != want {
		t.Fatalf("TypeName = %q, want %q", resp.TypeName, want)
	}
}
