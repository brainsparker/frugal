package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/frugalsh/frugal/internal/classifier"
	"github.com/frugalsh/frugal/internal/metrics"
	"github.com/frugalsh/frugal/internal/obs"
	"github.com/frugalsh/frugal/internal/provider"
	"github.com/frugalsh/frugal/internal/router"
	"github.com/frugalsh/frugal/internal/types"
	"github.com/frugalsh/frugal/internal/usecase"
)

const (
	maxFallbackAttempts          = 3
	defaultMaxCostPerRequestUSD  = 1.0
	maxDecisionBufferSize        = 10000
)

// maxCostPerRequestUSD reads the per-request spend cap once per process.
// A non-positive value disables the cap.
var maxCostPerRequestUSD = func() float64 {
	raw := os.Getenv("FRUGAL_MAX_COST_PER_REQUEST_USD")
	if raw == "" {
		return defaultMaxCostPerRequestUSD
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || v < 0 {
		return defaultMaxCostPerRequestUSD
	}
	return v
}()

const defaultDecisionBufferSize = 1000

// Handler serves the OpenAI-compatible API endpoints.
type Handler struct {
	classifier classifier.Classifier
	router     *router.Router
	registry   *provider.Registry
	// useCases is optional — a nil or empty registry disables use-case
	// routing and the /v1/bundles endpoints. The handler still works
	// exactly as before in that mode.
	useCases *usecase.Registry

	// Decision storage: the hot path posts to decisionCh (non-blocking send
	// with a drop-on-full policy so a slow /routing/explain consumer never
	// back-pressures chat requests). A single background goroutine drains
	// the channel into the ring buffer under mu.
	decisionCh   chan types.RoutingDecision
	mu           sync.Mutex
	decisions    []types.RoutingDecision
	decisionIdx  int
	lastDecision *types.RoutingDecision
}

func NewHandler(cls classifier.Classifier, rtr *router.Router, reg *provider.Registry) *Handler {
	return NewHandlerWithUseCases(cls, rtr, reg, nil)
}

// NewHandlerWithUseCases is the same as NewHandler but wires in a
// use-case registry. Passing nil preserves the legacy (chat-routing-only)
// behavior.
func NewHandlerWithUseCases(cls classifier.Classifier, rtr *router.Router, reg *provider.Registry, uc *usecase.Registry) *Handler {
	size := decisionBufferSizeFromEnv()
	h := &Handler{
		classifier: cls,
		router:     rtr,
		registry:   reg,
		useCases:   uc,
		decisionCh: make(chan types.RoutingDecision, size),
		decisions:  make([]types.RoutingDecision, 100),
	}
	go h.drainDecisions()
	return h
}

func decisionBufferSizeFromEnv() int {
	size := envIntOrDefault("FRUGAL_DECISION_BUFFER", defaultDecisionBufferSize)
	if size <= 0 {
		return defaultDecisionBufferSize
	}
	if size > maxDecisionBufferSize {
		return maxDecisionBufferSize
	}
	return size
}

// drainDecisions runs for the life of the handler, pumping decisions from the
// hot-path channel into the ring buffer. Runs on a single goroutine so the
// mutex never contends with request handling.
func (h *Handler) drainDecisions() {
	for d := range h.decisionCh {
		h.mu.Lock()
		h.decisions[h.decisionIdx%len(h.decisions)] = d
		h.decisionIdx++
		last := d
		h.lastDecision = &last
		h.mu.Unlock()
	}
}

// envIntOrDefault mirrors the CLI helper so the package is self-contained.
// Duplicated here rather than exported because the CLI version logs via slog
// which would introduce a cycle if imported.
func envIntOrDefault(key string, fallback int) int {
	if s, ok := lookupEnv(key); ok {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			return v
		}
	}
	return fallback
}

// lookupEnv is a tiny shim so tests can stub os.Getenv without a global.
var lookupEnv = func(key string) (string, bool) {
	v, ok := os.LookupEnv(key)
	return v, ok
}

// allowedFallbackModels returns a set of registered model names for
// allowlisting caller-supplied fallback chains.
func (h *Handler) allowedFallbackModels() map[string]struct{} {
	models := h.registry.AllModels()
	set := make(map[string]struct{}, len(models))
	for _, m := range models {
		set[m] = struct{}{}
	}
	return set
}

// recordDecision enqueues d for the background drain. The send is
// non-blocking: a slow drain or a packed channel drops the decision rather
// than stalling the hot path, which is the right trade-off — losing an
// observability point is cheaper than losing request latency.
func (h *Handler) recordDecision(d types.RoutingDecision) {
	select {
	case h.decisionCh <- d:
	default:
	}
}

const maxChatCompletionsBodyBytes int64 = 1 << 20 // 1 MiB

// ChatCompletions handles POST /v1/chat/completions
func (h *Handler) ChatCompletions(w http.ResponseWriter, r *http.Request) {
	req, err := decodeChatCompletionRequest(w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	quality := QualityFromContext(r.Context())
	fallbacks := FallbacksFromContext(r.Context())
	useCaseID := UseCaseFromContext(r.Context())

	var decision types.RoutingDecision
	var prov provider.Provider

	// Use-case routing: when X-Frugal-Use-Case is set, look up the bundle's
	// chat model for the requested quality tier and pin to it. Unknown use
	// case → 400 so caller typos surface immediately. Unknown tier or an
	// unregistered bundle model → fall through to the classifier rather than
	// hard-failing (degrades gracefully if a use case references a model
	// whose provider key isn't configured).
	if useCaseID != "" {
		if h.useCases == nil || h.useCases.Len() == 0 {
			writeError(w, http.StatusBadRequest, "X-Frugal-Use-Case set but no use cases are configured on this server")
			return
		}
		if _, ok := h.useCases.Get(useCaseID); !ok {
			known := strings.Join(h.useCases.IDs(), ", ")
			writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown use case %q; known: %s", useCaseID, known))
			return
		}
		if bundle, ok := h.useCases.Bundle(useCaseID, string(quality)); ok && bundle.Chat != "" {
			if p, err := h.registry.Resolve(bundle.Chat); err == nil {
				prov = p
				decision = types.RoutingDecision{
					SelectedModel:    bundle.Chat,
					SelectedProvider: p.Name(),
					Quality:          string(quality),
					Pinned:           true,
					Reason: fmt.Sprintf("pinned by use case %q at %s tier: %s",
						useCaseID, quality, strings.TrimSpace(bundle.Reason)),
				}
				w.Header().Set("X-Frugal-Use-Case", useCaseID)
			}
		}
	}

	// Model pinning: if model is not "auto" and not empty, try to resolve directly.
	// Skipped if use-case routing already resolved a provider.
	if prov == nil && req.Model != "" && req.Model != "auto" {
		if p, err := h.registry.Resolve(req.Model); err == nil {
			prov = p
			decision = types.RoutingDecision{
				SelectedModel:    req.Model,
				SelectedProvider: p.Name(),
				Quality:          string(quality),
				Pinned:           true,
				Reason:           fmt.Sprintf("model pinned to %s", req.Model),
			}
		}
	}

	// Route via classifier if not pinned
	if prov == nil {
		features := h.classifier.Classify(req)
		decision = h.router.Route(features, quality, fallbacks)

		if decision.SelectedModel == "" {
			writeError(w, http.StatusServiceUnavailable, "no suitable model found: "+decision.Reason)
			return
		}

		var err error
		prov, err = h.registry.GetProvider(decision.SelectedProvider)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "provider unavailable: "+err.Error())
			return
		}
	}

	// Per-request cost cap. Skip when the caller pinned a model (they know
	// what they asked for) and when the cap is disabled. The router already
	// records decision.EstimatedCost, so this is effectively free.
	if !decision.Pinned && maxCostPerRequestUSD > 0 && decision.EstimatedCost > maxCostPerRequestUSD {
		obs.L(r.Context()).Warn("rejecting request over cost cap",
			"estimated_cost_usd", decision.EstimatedCost,
			"cap_usd", maxCostPerRequestUSD,
			"model", decision.SelectedModel,
		)
		writeError(w, http.StatusPaymentRequired, "estimated request cost exceeds configured cap")
		return
	}

	h.recordDecision(decision)

	// Add routing info header
	w.Header().Set("X-Frugal-Model", decision.SelectedModel)
	w.Header().Set("X-Frugal-Provider", decision.SelectedProvider)
	if decision.RelaxedFrom != "" {
		w.Header().Set("X-Frugal-Relaxed-From", decision.RelaxedFrom)
		metrics.RoutingRelaxedTotal.WithLabelValues(decision.RelaxedFrom, decision.Quality).Inc()
	}

	start := time.Now()
	streamLabel := "nonstream"
	if req.Stream {
		streamLabel = "stream"
	}
	sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
	if req.Stream {
		h.handleStream(sw, r, prov, decision, req, fallbacks)
	} else {
		h.handleNonStream(sw, r, prov, decision, req, fallbacks)
	}
	metrics.RequestsTotal.WithLabelValues(
		decision.SelectedModel, decision.SelectedProvider, decision.Quality, metrics.StatusClass(sw.status),
	).Inc()
	metrics.ObserveDuration(metrics.RequestDurationSeconds, decision.SelectedModel, decision.SelectedProvider, streamLabel, time.Since(start))
	if decision.EstimatedCost > 0 {
		metrics.CostUSDTotal.WithLabelValues(decision.SelectedModel, decision.SelectedProvider).Add(decision.EstimatedCost)
	}
}

func decodeChatCompletionRequest(w http.ResponseWriter, r *http.Request) (*types.ChatCompletionRequest, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxChatCompletionsBodyBytes)
	defer r.Body.Close()

	// Unknown fields are accepted and forwarded to the OpenAI provider verbatim.
	// Real OpenAI SDKs routinely send fields the proxy would otherwise reject
	// (parallel_tool_calls, seed, reasoning_effort, service_tier, etc.), which
	// would break Frugal's "no code changes" promise.
	dec := json.NewDecoder(r.Body)

	var req types.ChatCompletionRequest
	if err := dec.Decode(&req); err != nil {
		var syntaxErr *json.SyntaxError
		var typeErr *json.UnmarshalTypeError
		switch {
		case errors.As(err, &syntaxErr):
			return nil, fmt.Errorf("malformed JSON")
		case errors.Is(err, io.EOF):
			return nil, fmt.Errorf("empty request body")
		case errors.As(err, &typeErr):
			return nil, fmt.Errorf("invalid value for field %q", typeErr.Field)
		default:
			return nil, err
		}
	}

	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("request body must contain a single JSON object")
	}

	return &req, nil
}

func (h *Handler) handleNonStream(w http.ResponseWriter, r *http.Request, prov provider.Provider, decision types.RoutingDecision, req *types.ChatCompletionRequest, fallbacks []string) {
	resp, err := prov.ChatCompletion(r.Context(), decision.SelectedModel, req)
	if err != nil {
		// Try fallback chain
		for _, fb := range boundedFallbacks(fallbacks, decision.SelectedModel, h.allowedFallbackModels()) {
			fbProv, fbErr := h.registry.Resolve(fb)
			if fbErr != nil {
				continue
			}
			resp, err = fbProv.ChatCompletion(r.Context(), fb, req)
			if err == nil {
				break
			}
			obs.L(r.Context()).Warn("fallback failed", "model", fb, "err", err)
		}
		if err != nil {
			obs.L(r.Context()).Error("upstream error", "model", decision.SelectedModel, "err", err)
			writeError(w, http.StatusBadGateway, sanitizedUpstreamMessage(err))
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// sanitizedUpstreamMessage returns a stable, operator-safe summary of an
// upstream failure. The full error — which can include the provider's
// response body and, in pathological cases, echoed request data — is logged
// but never written to the wire.
func sanitizedUpstreamMessage(err error) string {
	if err == nil {
		return "upstream error"
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "context deadline exceeded"), strings.Contains(msg, "timeout"):
		return "upstream timeout"
	case strings.Contains(msg, "429"), strings.Contains(msg, "rate limit"):
		return "upstream rate limited"
	case strings.Contains(msg, "401"), strings.Contains(msg, "403"):
		return "upstream rejected credentials"
	case strings.Contains(msg, "503"), strings.Contains(msg, "502"), strings.Contains(msg, "504"):
		return "upstream unavailable"
	default:
		return "upstream error"
	}
}

func (h *Handler) handleStream(w http.ResponseWriter, r *http.Request, prov provider.Provider, decision types.RoutingDecision, req *types.ChatCompletionRequest, fallbacks []string) {
	ch, first, err := openStreamWithFirstChunk(r.Context(), prov, decision.SelectedModel, req)
	if err != nil {
		// Handshake or first-chunk failure: walk the fallback chain. Once the
		// first chunk has been written to the client, any further error is
		// surfaced in-band (see streaming.go) — retry is no longer safe.
		for _, fb := range boundedFallbacks(fallbacks, decision.SelectedModel, h.allowedFallbackModels()) {
			fbProv, fbErr := h.registry.Resolve(fb)
			if fbErr != nil {
				continue
			}
			ch, first, err = openStreamWithFirstChunk(r.Context(), fbProv, fb, req)
			if err == nil {
				break
			}
			obs.L(r.Context()).Warn("fallback stream failed", "model", fb, "err", err)
		}
		if err != nil {
			obs.L(r.Context()).Error("upstream stream error", "model", decision.SelectedModel, "err", err)
			writeError(w, http.StatusBadGateway, sanitizedUpstreamMessage(err))
			return
		}
	}

	if err := streamResponseWithFirst(r.Context(), w, first, ch); err != nil {
		obs.L(r.Context()).Warn("stream write error", "err", err)
	}
}

// openStreamWithFirstChunk opens an upstream stream AND reads the first chunk
// synchronously so handshake-success-but-immediate-Err is still handled by
// the fallback chain. If the first chunk carries a Done (empty stream) that's
// still considered a success so the DONE terminator reaches the client.
func openStreamWithFirstChunk(ctx context.Context, prov provider.Provider, model string, req *types.ChatCompletionRequest) (<-chan provider.StreamChunk, *provider.StreamChunk, error) {
	ch, err := prov.ChatCompletionStream(ctx, model, req)
	if err != nil {
		return nil, nil, err
	}
	select {
	case first, ok := <-ch:
		if !ok {
			// Upstream closed the channel without emitting anything.
			// Treat as handshake failure so fallback runs.
			return nil, nil, fmt.Errorf("upstream closed stream before first chunk")
		}
		if first.Err != nil {
			return nil, nil, first.Err
		}
		return ch, &first, nil
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
}

// boundedFallbacks trims the caller-supplied fallback chain to registered
// models only, deduplicated, capped at maxFallbackAttempts, and skipping the
// routed model. Allow-listing against the registry prevents a client from
// crafting an `X-Frugal-Fallback` header that steers traffic to an expensive
// model (or a never-configured one) the operator did not authorize.
func boundedFallbacks(fallbacks []string, selectedModel string, allowed map[string]struct{}) []string {
	if len(fallbacks) == 0 {
		return nil
	}

	bounded := make([]string, 0, maxFallbackAttempts)
	seen := make(map[string]struct{}, len(fallbacks))
	for _, fb := range fallbacks {
		if len(bounded) >= maxFallbackAttempts {
			break
		}

		trimmed := strings.TrimSpace(fb)
		if trimmed == "" {
			continue
		}

		if strings.EqualFold(trimmed, selectedModel) {
			continue
		}

		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}

		if allowed != nil {
			if _, ok := allowed[trimmed]; !ok {
				obs.L(context.TODO()).Warn("ignoring unregistered fallback", "model", trimmed)
				continue
			}
		}

		bounded = append(bounded, trimmed)
	}

	return bounded
}

// ListModels handles GET /v1/models
func (h *Handler) ListModels(w http.ResponseWriter, r *http.Request) {
	models := h.registry.AllModels()

	type modelObj struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Created int64  `json:"created"`
		OwnedBy string `json:"owned_by"`
	}

	var data []modelObj
	for _, m := range models {
		data = append(data, modelObj{
			ID:      m,
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: "frugal",
		})
	}
	// Add the "auto" model
	data = append(data, modelObj{
		ID:      "auto",
		Object:  "model",
		Created: time.Now().Unix(),
		OwnedBy: "frugal",
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"object": "list",
		"data":   data,
	})
}

// RoutingExplain handles GET /v1/routing/explain
// ListBundles handles GET /v1/bundles — returns the set of known use cases
// with their bundles at every tier. Frontend-friendly for a "what can I
// route?" UI or a CLI lister.
func (h *Handler) ListBundles(w http.ResponseWriter, r *http.Request) {
	if h.useCases == nil || h.useCases.Len() == 0 {
		writeError(w, http.StatusNotFound, "no use cases configured")
		return
	}
	type bundleOut struct {
		Chat   string `json:"chat"`
		Search string `json:"search,omitempty"`
		Rerank string `json:"rerank,omitempty"`
		Reason string `json:"reason,omitempty"`
	}
	type caseOut struct {
		ID          string               `json:"id"`
		Description string               `json:"description"`
		Source      string               `json:"source"`
		AsOf        string               `json:"as_of"`
		Confidence  string               `json:"confidence"`
		Bundles     map[string]bundleOut `json:"bundles"`
	}
	out := make([]caseOut, 0, h.useCases.Len())
	for _, id := range h.useCases.IDs() {
		uc, _ := h.useCases.Get(id)
		bundles := map[string]bundleOut{}
		for tier, b := range uc.Bundles {
			bundles[tier] = bundleOut{Chat: b.Chat, Search: b.Search, Rerank: b.Rerank, Reason: b.Reason}
		}
		out = append(out, caseOut{
			ID: uc.ID, Description: uc.Description, Source: uc.Source,
			AsOf: uc.AsOf, Confidence: uc.Confidence, Bundles: bundles,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": out})
}

// GetBundle handles GET /v1/bundles/{use-case}?quality=TIER — returns the
// (capability → model) map for one use case at one tier. Tier defaults to
// balanced when the query param is absent.
func (h *Handler) GetBundle(w http.ResponseWriter, r *http.Request) {
	if h.useCases == nil || h.useCases.Len() == 0 {
		writeError(w, http.StatusNotFound, "no use cases configured")
		return
	}
	id := chi.URLParam(r, "useCase")
	uc, ok := h.useCases.Get(id)
	if !ok {
		known := strings.Join(h.useCases.IDs(), ", ")
		writeError(w, http.StatusNotFound, fmt.Sprintf("unknown use case %q; known: %s", id, known))
		return
	}
	tier := r.URL.Query().Get("quality")
	if tier == "" {
		tier = "balanced"
	}
	bundle, ok := uc.Bundles[tier]
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("use case %q has no %q tier", id, tier))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"use_case":   uc.ID,
		"quality":    tier,
		"chat":       bundle.Chat,
		"search":     bundle.Search,
		"rerank":     bundle.Rerank,
		"reason":     strings.TrimSpace(bundle.Reason),
		"source":     uc.Source,
		"as_of":      uc.AsOf,
		"confidence": uc.Confidence,
	})
}

func (h *Handler) RoutingExplain(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	d := h.lastDecision
	h.mu.Unlock()

	if d == nil {
		writeError(w, http.StatusNotFound, "no routing decisions recorded yet")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(d)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"message": msg,
			"type":    "frugal_error",
		},
	})
}
