package provider

import (
	"context"
	"testing"

	datasourcefw "github.com/hashicorp/terraform-plugin-framework/datasource"
)

func TestSeristackDataSourceSchema(t *testing.T) {
	t.Parallel()

	d, ok := NewSeristackDataSource().(*SeristackDataSource)
	if !ok {
		t.Fatalf("expected *SeristackDataSource")
	}

	var resp datasourcefw.SchemaResponse
	d.Schema(context.Background(), datasourcefw.SchemaRequest{}, &resp)

	if _, ok := resp.Schema.Attributes["type"]; !ok {
		t.Fatalf("data source schema missing 'type' attribute")
	}
	if _, ok := resp.Schema.Attributes["vars"]; !ok {
		t.Fatalf("data source schema missing 'vars' attribute")
	}
	if _, ok := resp.Schema.Attributes["output"]; !ok {
		t.Fatalf("data source schema missing 'output' attribute")
	}
}
