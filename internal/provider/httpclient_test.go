package provider

import (
	"net/http"
	"testing"
)

func TestNewHTTPClient_ConfiguresDefensiveTransport(t *testing.T) {
	client := NewHTTPClient()
	if client == nil {
		t.Fatal("expected non-nil client")
	}

	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", client.Transport)
	}

	if transport.ResponseHeaderTimeout != defaultResponseHeaderTimeout {
		t.Fatalf("expected ResponseHeaderTimeout %s, got %s", defaultResponseHeaderTimeout, transport.ResponseHeaderTimeout)
	}
	if transport.TLSHandshakeTimeout != defaultTLSHandshakeTimeout {
		t.Fatalf("expected TLSHandshakeTimeout %s, got %s", defaultTLSHandshakeTimeout, transport.TLSHandshakeTimeout)
	}
	if transport.IdleConnTimeout != defaultIdleConnTimeout {
		t.Fatalf("expected IdleConnTimeout %s, got %s", defaultIdleConnTimeout, transport.IdleConnTimeout)
	}
}
