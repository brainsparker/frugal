package proxy

import (
	"strings"
	"testing"

	"net/http/httptest"

	"github.com/frugalsh/frugal/internal/provider"
	"github.com/frugalsh/frugal/internal/types"
)

func TestStreamResponse_EmitsDoneWhenChannelClosesWithoutDoneChunk(t *testing.T) {
	w := httptest.NewRecorder()
	ch := make(chan provider.StreamChunk, 1)

	ch <- provider.StreamChunk{
		Data: &types.ChatCompletionChunk{
			ID:     "chatcmpl-stream",
			Object: "chat.completion.chunk",
			Model:  "mock-model",
			Choices: []types.ChunkChoice{
				{Index: 0, Delta: types.MessageDelta{Content: "hello"}},
			},
		},
	}
	close(ch)

	if err := streamResponse(w, ch); err != nil {
		t.Fatalf("streamResponse returned error: %v", err)
	}

	body := w.Body.String()
	if !strings.Contains(body, "data: [DONE]") {
		t.Fatalf("expected [DONE] terminator, got body: %s", body)
	}
}
