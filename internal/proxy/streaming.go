package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/frugalsh/frugal/internal/obs"
	"github.com/frugalsh/frugal/internal/provider"
)

// streamResponseWithFirst writes a buffered first chunk followed by the rest
// of the stream. Splitting the first chunk out lets the handler retry to a
// fallback model on first-chunk failure before any bytes are sent to the
// client; once the first chunk lands, the stream is irrevocable.
func streamResponseWithFirst(ctx context.Context, w http.ResponseWriter, first *provider.StreamChunk, ch <-chan provider.StreamChunk) error {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return fmt.Errorf("streaming not supported")
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	bytesWritten := false
	if first != nil {
		if err := writeSSEChunk(ctx, w, flusher, *first); err != nil {
			return err
		}
		bytesWritten = true
		if first.Done {
			return nil
		}
	}

	gotTerminator := false
	for chunk := range ch {
		if err := writeSSEChunk(ctx, w, flusher, chunk); err != nil {
			return err
		}
		bytesWritten = true
		if chunk.Done {
			gotTerminator = true
			return chunk.Err
		}
		if chunk.Err != nil {
			return chunk.Err
		}
	}

	// Channel closed. If the provider never sent a Done/Err terminator and
	// we already shipped some bytes, the client would otherwise hang waiting
	// for the next chunk. Emit an error frame so the client knows the stream
	// was cut short, followed by the standard [DONE] marker so its SSE
	// parser terminates cleanly.
	if bytesWritten && !gotTerminator {
		errFrame := `{"error":{"message":"upstream closed stream prematurely","type":"frugal_error"}}`
		if _, err := fmt.Fprintf(w, "data: %s\n\n", errFrame); err != nil {
			return err
		}
		flusher.Flush()
	}
	if _, err := fmt.Fprint(w, "data: [DONE]\n\n"); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

// streamResponse is retained for tests that don't need the split first-chunk
// path. Prefer streamResponseWithFirst in production handler code.
func streamResponse(ctx context.Context, w http.ResponseWriter, ch <-chan provider.StreamChunk) error {
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
			obs.L(ctx).Error("stream upstream error", "err", chunk.Err)
			errData := fmt.Sprintf(`{"error":{"message":%q,"type":"frugal_error"}}`, sanitizedUpstreamMessage(chunk.Err))
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

// writeSSEChunk serializes a single StreamChunk to the wire. Error chunks
// collapse to a sanitized frame; Done triggers a [DONE] terminator; normal
// chunks emit a JSON event.
func writeSSEChunk(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, chunk provider.StreamChunk) error {
	if chunk.Err != nil {
		obs.L(ctx).Error("stream upstream error", "err", chunk.Err)
		errData := fmt.Sprintf(`{"error":{"message":%q,"type":"frugal_error"}}`, sanitizedUpstreamMessage(chunk.Err))
		if _, err := fmt.Fprintf(w, "data: %s\n\n", errData); err != nil {
			return err
		}
		flusher.Flush()
		return nil
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
		return nil
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}
