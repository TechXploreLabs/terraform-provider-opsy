package provider

import (
	"context"
	"testing"

	resourcefw "github.com/hashicorp/terraform-plugin-framework/resource"
)

func TestSeristackResourceSchema(t *testing.T) {
	t.Parallel()

	r, ok := NewSeristackResource().(*SeristackResource)
	if !ok {
		t.Fatalf("expected *SeristackResource")
	}

	var resp resourcefw.SchemaResponse
	r.Schema(context.Background(), resourcefw.SchemaRequest{}, &resp)

	if _, ok := resp.Schema.Attributes["type"]; !ok {
		t.Fatalf("resource schema missing 'type' attribute")
	}
	if _, ok := resp.Schema.Attributes["vars"]; !ok {
		t.Fatalf("resource schema missing 'vars' attribute")
	}
	if _, ok := resp.Schema.Attributes["output"]; !ok {
		t.Fatalf("resource schema missing 'output' attribute")
	}
}
