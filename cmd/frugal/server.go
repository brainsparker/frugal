package main

import (
	"log"
	"net/http"
	"os"
	"strconv"
	"time"
)

const (
	defaultReadHeaderTimeout = 5 * time.Second
	defaultReadTimeout       = 15 * time.Second
	defaultWriteTimeout      = 30 * time.Second
	defaultIdleTimeout       = 60 * time.Second
	defaultMaxHeaderBytes    = 1 << 20 // 1 MiB
)

func newHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: durationFromEnv("FRUGAL_HTTP_READ_HEADER_TIMEOUT", defaultReadHeaderTimeout),
		ReadTimeout:       durationFromEnv("FRUGAL_HTTP_READ_TIMEOUT", defaultReadTimeout),
		WriteTimeout:      durationFromEnv("FRUGAL_HTTP_WRITE_TIMEOUT", defaultWriteTimeout),
		IdleTimeout:       durationFromEnv("FRUGAL_HTTP_IDLE_TIMEOUT", defaultIdleTimeout),
		MaxHeaderBytes:    intFromEnv("FRUGAL_HTTP_MAX_HEADER_BYTES", defaultMaxHeaderBytes),
	}
}

func durationFromEnv(name string, fallback time.Duration) time.Duration {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 {
		log.Printf("warning: invalid %s=%q, using default %s", name, value, fallback)
		return fallback
	}
	return d
}

func intFromEnv(name string, fallback int) int {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 {
		log.Printf("warning: invalid %s=%q, using default %d", name, value, fallback)
		return fallback
	}
	return n
}
