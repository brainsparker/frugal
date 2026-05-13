package provider

import (
	"context"
	"testing"

	"github.com/frugalsh/frugal/internal/types"
)

type fakeProvider struct {
	name   string
	models []string
}

func (f *fakeProvider) Name() string { return f.name }
func (f *fakeProvider) Models() []string { return f.models }
func (f *fakeProvider) ChatCompletion(_ context.Context, _ string, _ *types.ChatCompletionRequest) (*types.ChatCompletionResponse, error) {
	return nil, nil
}
func (f *fakeProvider) ChatCompletionStream(_ context.Context, _ string, _ *types.ChatCompletionRequest) (<-chan StreamChunk, error) {
	ch := make(chan StreamChunk)
	close(ch)
	return ch, nil
}

func TestAllModelsSorted(t *testing.T) {
	r := NewRegistry()
	r.Register(&fakeProvider{name: "p1", models: []string{"z-model", "a-model"}})
	r.Register(&fakeProvider{name: "p2", models: []string{"m-model"}})

	got := r.AllModels()
	want := []string{"a-model", "m-model", "z-model"}
	if len(got) != len(want) {
		t.Fatalf("AllModels length = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("AllModels[%d] = %q, want %q; full=%v", i, got[i], want[i], got)
		}
	}
}
