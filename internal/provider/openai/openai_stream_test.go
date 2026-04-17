package openai

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/frugalsh/frugal/internal/types"
)

func TestChatCompletionStream_AllowsLargeSSELines(t *testing.T) {
	large := strings.Repeat("x", 70*1024)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"gpt-4o-mini\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"%s\"}}]}\n\n", large)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer ts.Close()

	p := New("test-key", ts.URL, []string{"gpt-4o-mini"})
	p.client = ts.Client()

	ch, err := p.ChatCompletionStream(context.Background(), "gpt-4o-mini", &types.ChatCompletionRequest{})
	if err != nil {
		t.Fatalf("ChatCompletionStream returned error: %v", err)
	}

	var gotData, gotDone bool
	for chunk := range ch {
		if chunk.Err != nil {
			t.Fatalf("stream chunk error: %v", chunk.Err)
		}
		if chunk.Data != nil {
			gotData = true
		}
		if chunk.Done {
			gotDone = true
		}
	}

	if !gotData {
		t.Fatal("expected at least one data chunk")
	}
	if !gotDone {
		t.Fatal("expected done chunk")
	}
}

