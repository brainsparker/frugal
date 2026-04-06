package proxy

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/frugalsh/frugal/internal/provider"
)

// streamResponse writes SSE chunks from a provider stream channel to the HTTP response.
func streamResponse(w http.ResponseWriter, ch <-chan provider.StreamChunk) error {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return fmt.Errorf("streaming not supported")
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	for chunk := range ch {
		if chunk.Err != nil {
			errData := fmt.Sprintf(`{"error":{"message":%q}}`, chunk.Err.Error())
			fmt.Fprintf(w, "data: %s\n\n", errData)
			flusher.Flush()
			return chunk.Err
		}

		if chunk.Done {
			fmt.Fprint(w, "data: [DONE]\n\n")
			flusher.Flush()
			return nil
		}

		data, err := json.Marshal(chunk.Data)
		if err != nil {
			continue
		}
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}

	return nil
}
