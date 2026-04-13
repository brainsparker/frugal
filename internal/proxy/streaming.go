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
			if _, err := fmt.Fprintf(w, "data: %s\n\n", errData); err != nil {
				return err
			}
			flusher.Flush()
			return chunk.Err
		}

		if chunk.Done {
			if _, err := fmt.Fprint(w, "data: [DONE]\n\n"); err != nil {
				return err
			}
			flusher.Flush()
			return nil
		}

		data, err := json.Marshal(chunk.Data)
		if err != nil {
			continue
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
			return err
		}
		flusher.Flush()
	}

	// Some providers close the stream channel without sending an explicit Done chunk.
	// Emit the OpenAI-compatible terminator so clients don't wait indefinitely.
	if _, err := fmt.Fprint(w, "data: [DONE]\n\n"); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}
