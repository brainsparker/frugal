package main

import (
	"net/http"
	"os"
	"testing"
	"time"
)

func TestNewHTTPServerDefaults(t *testing.T) {
	t.Setenv("FRUGAL_HTTP_READ_HEADER_TIMEOUT", "")
	t.Setenv("FRUGAL_HTTP_READ_TIMEOUT", "")
	t.Setenv("FRUGAL_HTTP_WRITE_TIMEOUT", "")
	t.Setenv("FRUGAL_HTTP_IDLE_TIMEOUT", "")
	t.Setenv("FRUGAL_HTTP_MAX_HEADER_BYTES", "")

	srv := newHTTPServer(":8080", http.NewServeMux())

	if srv.ReadHeaderTimeout != defaultReadHeaderTimeout {
		t.Fatalf("ReadHeaderTimeout = %s, want %s", srv.ReadHeaderTimeout, defaultReadHeaderTimeout)
	}
	if srv.ReadTimeout != defaultReadTimeout {
		t.Fatalf("ReadTimeout = %s, want %s", srv.ReadTimeout, defaultReadTimeout)
	}
	if srv.WriteTimeout != defaultWriteTimeout {
		t.Fatalf("WriteTimeout = %s, want %s", srv.WriteTimeout, defaultWriteTimeout)
	}
	if srv.IdleTimeout != defaultIdleTimeout {
		t.Fatalf("IdleTimeout = %s, want %s", srv.IdleTimeout, defaultIdleTimeout)
	}
	if srv.MaxHeaderBytes != defaultMaxHeaderBytes {
		t.Fatalf("MaxHeaderBytes = %d, want %d", srv.MaxHeaderBytes, defaultMaxHeaderBytes)
	}
}

func TestNewHTTPServerEnvOverrides(t *testing.T) {
	t.Setenv("FRUGAL_HTTP_READ_HEADER_TIMEOUT", "2s")
	t.Setenv("FRUGAL_HTTP_READ_TIMEOUT", "12s")
	t.Setenv("FRUGAL_HTTP_WRITE_TIMEOUT", "20s")
	t.Setenv("FRUGAL_HTTP_IDLE_TIMEOUT", "45s")
	t.Setenv("FRUGAL_HTTP_MAX_HEADER_BYTES", "65536")

	srv := newHTTPServer(":8080", http.NewServeMux())

	if srv.ReadHeaderTimeout != 2*time.Second {
		t.Fatalf("ReadHeaderTimeout = %s, want 2s", srv.ReadHeaderTimeout)
	}
	if srv.ReadTimeout != 12*time.Second {
		t.Fatalf("ReadTimeout = %s, want 12s", srv.ReadTimeout)
	}
	if srv.WriteTimeout != 20*time.Second {
		t.Fatalf("WriteTimeout = %s, want 20s", srv.WriteTimeout)
	}
	if srv.IdleTimeout != 45*time.Second {
		t.Fatalf("IdleTimeout = %s, want 45s", srv.IdleTimeout)
	}
	if srv.MaxHeaderBytes != 65536 {
		t.Fatalf("MaxHeaderBytes = %d, want 65536", srv.MaxHeaderBytes)
	}
}

func TestInvalidEnvFallsBackToDefaults(t *testing.T) {
	t.Setenv("FRUGAL_HTTP_READ_TIMEOUT", "banana")
	t.Setenv("FRUGAL_HTTP_MAX_HEADER_BYTES", "-5")

	if got := durationFromEnv("FRUGAL_HTTP_READ_TIMEOUT", defaultReadTimeout); got != defaultReadTimeout {
		t.Fatalf("durationFromEnv invalid value = %s, want %s", got, defaultReadTimeout)
	}
	if got := intFromEnv("FRUGAL_HTTP_MAX_HEADER_BYTES", defaultMaxHeaderBytes); got != defaultMaxHeaderBytes {
		t.Fatalf("intFromEnv invalid value = %d, want %d", got, defaultMaxHeaderBytes)
	}
}

func TestDurationFromEnvPositiveRequired(t *testing.T) {
	t.Setenv("FRUGAL_HTTP_IDLE_TIMEOUT", "0s")
	if got := durationFromEnv("FRUGAL_HTTP_IDLE_TIMEOUT", defaultIdleTimeout); got != defaultIdleTimeout {
		t.Fatalf("durationFromEnv zero value = %s, want fallback %s", got, defaultIdleTimeout)
	}
}

func TestIntFromEnvPositiveRequired(t *testing.T) {
	t.Setenv("FRUGAL_HTTP_MAX_HEADER_BYTES", "0")
	if got := intFromEnv("FRUGAL_HTTP_MAX_HEADER_BYTES", defaultMaxHeaderBytes); got != defaultMaxHeaderBytes {
		t.Fatalf("intFromEnv zero value = %d, want fallback %d", got, defaultMaxHeaderBytes)
	}
}

func TestHelpersDoNotMutateEnvironment(t *testing.T) {
	t.Setenv("FRUGAL_HTTP_READ_TIMEOUT", "3s")
	_ = durationFromEnv("FRUGAL_HTTP_READ_TIMEOUT", defaultReadTimeout)
	if got := os.Getenv("FRUGAL_HTTP_READ_TIMEOUT"); got != "3s" {
		t.Fatalf("env mutated, got %q want %q", got, "3s")
	}
}
