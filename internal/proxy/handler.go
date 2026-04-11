package proxy

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/frugalsh/frugal/internal/classifier"
	"github.com/frugalsh/frugal/internal/provider"
	"github.com/frugalsh/frugal/internal/router"
	"github.com/frugalsh/frugal/internal/types"
)

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

func (h *Handler) recordDecision(d types.RoutingDecision) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.decisions[h.decisionIdx%len(h.decisions)] = d
	h.decisionIdx++
	h.lastDecision = &d
}

// ChatCompletions handles POST /v1/chat/completions
func (h *Handler) ChatCompletions(w http.ResponseWriter, r *http.Request) {
	var req types.ChatCompletionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	quality := QualityFromContext(r.Context())
	fallbacks := FallbacksFromContext(r.Context())

	var decision types.RoutingDecision
	var prov provider.Provider

	// Model pinning: if model is not "auto" and not empty, resolve directly.
	// Explicit model pins should fail fast when unknown rather than silently rerouting.
	if req.Model != "" && req.Model != "auto" {
		p, err := h.registry.Resolve(req.Model)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown model %q", req.Model))
			return
		}

		prov = p
		decision = types.RoutingDecision{
			SelectedModel:    req.Model,
			SelectedProvider: p.Name(),
			Quality:          string(quality),
			Pinned:           true,
			Reason:           fmt.Sprintf("model pinned to %s", req.Model),
		}
	}

	// Route via classifier if not pinned
	if prov == nil {
		features := h.classifier.Classify(&req)
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

	h.recordDecision(decision)

	// Add routing info header
	w.Header().Set("X-Frugal-Model", decision.SelectedModel)
	w.Header().Set("X-Frugal-Provider", decision.SelectedProvider)

	if req.Stream {
		h.handleStream(w, r, prov, decision, &req, fallbacks)
	} else {
		h.handleNonStream(w, r, prov, decision, &req, fallbacks)
	}
}

func (h *Handler) handleNonStream(w http.ResponseWriter, r *http.Request, prov provider.Provider, decision types.RoutingDecision, req *types.ChatCompletionRequest, fallbacks []string) {
	resp, err := prov.ChatCompletion(r.Context(), decision.SelectedModel, req)
	if err != nil {
		// Try fallback chain
		for _, fb := range fallbacks {
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
			writeError(w, http.StatusBadGateway, "upstream error: "+err.Error())
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *Handler) handleStream(w http.ResponseWriter, r *http.Request, prov provider.Provider, decision types.RoutingDecision, req *types.ChatCompletionRequest, fallbacks []string) {
	ch, err := prov.ChatCompletionStream(r.Context(), decision.SelectedModel, req)
	if err != nil {
		// Try fallback chain
		for _, fb := range fallbacks {
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
			writeError(w, http.StatusBadGateway, "upstream stream error: "+err.Error())
			return
		}
	}

	if err := streamResponse(w, ch); err != nil {
		log.Printf("stream error: %v", err)
	}
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
