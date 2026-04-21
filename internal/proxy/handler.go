package proxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/frugalsh/frugal/internal/classifier"
	"github.com/frugalsh/frugal/internal/provider"
	"github.com/frugalsh/frugal/internal/router"
	"github.com/frugalsh/frugal/internal/types"
)

const (
	maxFallbackAttempts          = 3
	defaultMaxCostPerRequestUSD  = 1.0
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

// Handler serves the OpenAI-compatible API endpoints.
type Handler struct {
	classifier classifier.Classifier
	router     *router.Router
	registry   *provider.Registry

	// Ring buffer of recent routing decisions for /v1/routing/explain
	mu             sync.Mutex
	decisions      []types.RoutingDecision
	decisionIdx    int
	lastDecision   *types.RoutingDecision
}

func NewHandler(cls classifier.Classifier, rtr *router.Router, reg *provider.Registry) *Handler {
	return &Handler{
		classifier: cls,
		router:     rtr,
		registry:   reg,
		decisions:  make([]types.RoutingDecision, 100),
	}
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

func (h *Handler) recordDecision(d types.RoutingDecision) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.decisions[h.decisionIdx%len(h.decisions)] = d
	h.decisionIdx++
	h.lastDecision = &d
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

	var decision types.RoutingDecision
	var prov provider.Provider

	// Model pinning: if model is not "auto" and not empty, try to resolve directly
	if req.Model != "" && req.Model != "auto" {
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
		log.Printf("rejecting request: estimated cost $%.4f exceeds cap $%.2f (model=%s)",
			decision.EstimatedCost, maxCostPerRequestUSD, decision.SelectedModel)
		writeError(w, http.StatusPaymentRequired, "estimated request cost exceeds configured cap")
		return
	}

	h.recordDecision(decision)

	// Add routing info header
	w.Header().Set("X-Frugal-Model", decision.SelectedModel)
	w.Header().Set("X-Frugal-Provider", decision.SelectedProvider)

	if req.Stream {
		h.handleStream(w, r, prov, decision, req, fallbacks)
	} else {
		h.handleNonStream(w, r, prov, decision, req, fallbacks)
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
			log.Printf("fallback %s failed: %v", fb, err)
		}
		if err != nil {
			log.Printf("upstream error on %s: %v", decision.SelectedModel, err)
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
	ch, err := prov.ChatCompletionStream(r.Context(), decision.SelectedModel, req)
	if err != nil {
		// Try fallback chain
		for _, fb := range boundedFallbacks(fallbacks, decision.SelectedModel, h.allowedFallbackModels()) {
			fbProv, fbErr := h.registry.Resolve(fb)
			if fbErr != nil {
				continue
			}
			ch, err = fbProv.ChatCompletionStream(r.Context(), fb, req)
			if err == nil {
				break
			}
			log.Printf("fallback stream %s failed: %v", fb, err)
		}
		if err != nil {
			log.Printf("upstream stream error on %s: %v", decision.SelectedModel, err)
			writeError(w, http.StatusBadGateway, sanitizedUpstreamMessage(err))
			return
		}
	}

	if err := streamResponse(w, ch); err != nil {
		log.Printf("stream error: %v", err)
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
				log.Printf("ignoring fallback %q: not a registered model", trimmed)
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
