package provider

import (
	"context"
	"testing"
)

func TestProviderExposesCoreComponents(t *testing.T) {
	t.Parallel()

	p := New("test")().(*OpsyProvider)

	if len(p.Resources(context.Background())) == 0 {
		t.Fatalf("expected provider to expose at least one resource")
	}
	if len(p.DataSources(context.Background())) == 0 {
		t.Fatalf("expected provider to expose at least one data source")
	}
	if len(p.Functions(context.Background())) == 0 {
		t.Fatalf("expected provider to expose at least one function")
	}
}
