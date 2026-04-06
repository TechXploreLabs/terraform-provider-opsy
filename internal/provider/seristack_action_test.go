package provider

import (
	"context"
	"testing"

	actionfw "github.com/hashicorp/terraform-plugin-framework/action"
)

func TestSeristackActionSchema(t *testing.T) {
	t.Parallel()

	a, ok := NewSeristackAction().(*SeristackAction)
	if !ok {
		t.Fatalf("expected *SeristackAction")
	}

	var resp actionfw.SchemaResponse
	a.Schema(context.Background(), actionfw.SchemaRequest{}, &resp)

	if _, ok := resp.Schema.Attributes["type"]; !ok {
		t.Fatalf("action schema missing 'type' attribute")
	}
	if _, ok := resp.Schema.Attributes["stackname"]; !ok {
		t.Fatalf("action schema missing 'stackname' attribute")
	}
	if _, ok := resp.Schema.Attributes["vars"]; !ok {
		t.Fatalf("action schema missing 'vars' attribute")
	}
}
