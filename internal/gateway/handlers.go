package gateway

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"mime"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/zltl/aiyolo/internal/auth"
	"github.com/zltl/aiyolo/internal/domain"
	proxytransport "github.com/zltl/aiyolo/internal/proxy"
	"github.com/zltl/aiyolo/internal/storage"
)

type Handler struct {
	store      storage.Store
	transports *proxytransport.TransportFactory
	cacheMu    sync.RWMutex
	cache      map[string]responseCacheEntry
}

type responseCacheEntry struct {
	StatusCode    int
	Headers       http.Header
	Body          []byte
	CreatedAt     time.Time
	TTLSeconds    int
	ProviderID    string
	ModelAlias    string
	UpstreamModel string
	Currency      string
	Protocol      string
	Endpoint      string
}

type responseCacheControl struct {
	Enabled    bool
	Clear      bool
	TTLSeconds int
}

const defaultResponseCacheTTLSeconds = 60

type modelListPricing struct {
	Prompt          string `json:"prompt,omitempty"`
	Completion      string `json:"completion,omitempty"`
	InputCacheRead  string `json:"input_cache_read,omitempty"`
	InputCacheWrite string `json:"input_cache_write,omitempty"`
}

type modelListEntry struct {
	ID                  string            `json:"id"`
	Object              string            `json:"object"`
	Created             int64             `json:"created"`
	OwnedBy             string            `json:"owned_by"`
	ContextLength       int               `json:"context_length,omitempty"`
	Pricing             *modelListPricing `json:"pricing,omitempty"`
	SupportedParameters []string          `json:"supported_parameters,omitempty"`
}

type routeCandidate struct {
	ModelIndex int
	Route      domain.ModelRoute
	Provider   domain.Provider
	Profile    domain.ProxyProfile
	Pricing    domain.PricingRule
}

type routerMetadataEndpoint struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Selected bool   `json:"selected"`
}

type routerMetadataAttempt struct {
	Index        int    `json:"index"`
	Provider     string `json:"provider"`
	Model        string `json:"model"`
	Status       string `json:"status"`
	StatusCode   int    `json:"status_code,omitempty"`
	FailureClass string `json:"failure_class,omitempty"`
}

type routerMetadata struct {
	Requested     string `json:"requested"`
	ResolvedModel string `json:"resolved_model"`
	Strategy      string `json:"strategy"`
	Attempt       int    `json:"attempt"`
	IsByok        bool   `json:"is_byok"`
	Summary       string `json:"summary"`
	Endpoints     struct {
		Total     int                      `json:"total"`
		Available []routerMetadataEndpoint `json:"available"`
	} `json:"endpoints"`
	Attempts []routerMetadataAttempt `json:"attempts"`
}

type providerPriceLimits struct {
	Prompt     float64
	Completion float64
}

type providerPreferences struct {
	Order             []string
	Only              []string
	Ignore            []string
	Sort              string
	AllowFallbacks    bool
	HasAllowFallbacks bool
	RequireParameters bool
	HasMaxPrice       bool
	MaxPrice          providerPriceLimits
	DataCollection    string
	HasDataCollection bool
	ZDR               bool
	HasZDR            bool
}

type compatibleRequest struct {
	Model              string
	FallbackModels     []string
	Stream             bool
	Payload            map[string]any
	RequiredParameters []string
	Provider           providerPreferences
}

type requestError struct {
	Status  int
	Code    string
	Message string
}

func (err *requestError) Error() string {
	if err == nil {
		return ""
	}
	return err.Message
}

func NewHandler(store storage.Store, transports *proxytransport.TransportFactory) *Handler {
	return &Handler{store: store, transports: transports, cache: make(map[string]responseCacheEntry)}
}

func (handler *Handler) Routes() http.Handler {
	router := chi.NewRouter()
	router.Get("/models", handler.listModels)
	router.Get("/key", handler.keyInfo)
	router.Get("/generation", handler.generation)
	router.Post("/chat/completions", handler.forwardOpenAI("/v1/chat/completions"))
	router.Post("/completions", handler.forwardOpenAI("/v1/completions"))
	router.Post("/embeddings", handler.forwardOpenAI("/v1/embeddings"))
	router.Get("/responses", handler.responsesWebsocketUnsupported)
	router.Post("/responses", handler.forwardOpenAI("/v1/responses"))
	router.Post("/messages", handler.forwardAnthropic("/v1/messages"))
	router.Post("/messages/count_tokens", handler.forwardAnthropic("/v1/messages/count_tokens"))
	return router
}

func (handler *Handler) responsesWebsocketUnsupported(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Connection", "close")
	w.WriteHeader(http.StatusUpgradeRequired)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": "Responses websocket transport is not supported by this gateway; retry with HTTP Responses", "type": "invalid_request_error", "code": "responses_websocket_unsupported"}})
}

func (handler *Handler) listModels(w http.ResponseWriter, r *http.Request) {
	if _, ok := handler.authenticate(w, r, "", ""); !ok {
		return
	}
	routes, err := handler.store.ListModelRoutes(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	response := struct {
		Object string           `json:"object"`
		Data   []modelListEntry `json:"data"`
	}{Object: "list"}
	now := time.Now().Unix()
	for _, route := range routes {
		provider, providerErr := handler.store.GetProvider(r.Context(), route.ProviderID)
		if providerErr != nil && !errors.Is(providerErr, storage.ErrNotFound) {
			writeJSONError(w, http.StatusInternalServerError, "store_error", providerErr.Error())
			return
		}
		pricingRule, err := handler.pricingRuleForRoute(r.Context(), route)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "store_error", err.Error())
			return
		}
		response.Data = append(response.Data, modelListEntry{
			ID:                  route.PublicName,
			Object:              "model",
			Created:             now,
			OwnedBy:             route.ProviderID,
			ContextLength:       route.ContextTokens,
			Pricing:             modelPricingFromRule(pricingRule),
			SupportedParameters: supportedParameters(route, provider),
		})
	}
	writeJSON(w, http.StatusOK, response)
}

func (handler *Handler) forwardOpenAI(endpoint string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		handler.forwardCompatible(w, r, endpoint, domain.ProtocolOpenAI)
	}
}

func (handler *Handler) forwardAnthropic(endpoint string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		handler.forwardCompatible(w, r, endpoint, domain.ProtocolAnthropic)
	}
}

func (handler *Handler) forwardCompatible(w http.ResponseWriter, r *http.Request, endpoint string, protocol string) {
	started := time.Now()
	requestID := requestID(r)
	w.Header().Set("X-Generation-Id", requestID)
	includeRouterMetadata := metadataRequested(r)
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<20))
	if err != nil {
		writeProtocolError(w, http.StatusBadRequest, "read_body_failed", err.Error(), protocol, nil)
		return
	}
	_ = r.Body.Close()
	if endpoint == "/v1/responses" {
		body, err = normalizeResponsesRequestBody(body)
		if err != nil {
			writeProtocolError(w, http.StatusBadRequest, "invalid_json", err.Error(), protocol, nil)
			return
		}
	}
	request, err := parseCompatibleRequest(body)
	if err != nil {
		var reqErr *requestError
		if errors.As(err, &reqErr) {
			writeProtocolError(w, reqErr.Status, reqErr.Code, reqErr.Message, protocol, nil)
			return
		}
		writeProtocolError(w, http.StatusBadRequest, "invalid_json", err.Error(), protocol, nil)
		return
	}
	subject, ok := handler.authenticate(w, r, protocol, request.Model)
	if !ok {
		return
	}
	if !handler.authorizeAdditionalModels(w, subject, protocol, request.FallbackModels) {
		return
	}
	cacheControl, err := parseResponseCacheControl(r, request.Stream)
	if err != nil {
		var reqErr *requestError
		if errors.As(err, &reqErr) {
			writeProtocolError(w, reqErr.Status, reqErr.Code, reqErr.Message, protocol, nil)
			return
		}
		writeProtocolError(w, http.StatusBadRequest, "invalid_cache_settings", err.Error(), protocol, nil)
		return
	}
	cacheKey := ""
	if cacheControl.Enabled || cacheControl.Clear {
		cacheKey = responseCacheKey(subject.APIKeyID, request, endpoint, body)
		if cacheControl.Clear {
			handler.deleteResponseCache(cacheKey)
		}
		if cacheControl.Enabled {
			if entry, hit := handler.loadResponseCache(cacheKey, time.Now().UTC()); hit {
				usage, writeErr := writeCachedResponse(w, entry, protocol)
				if writeErr != nil {
					log.Printf("write cached response request_id=%s err=%v", requestID, writeErr)
				}
				usage.RequestID = requestID
				usage.UserID = subject.UserID
				usage.APIKeyID = subject.APIKeyID
				usage.ProviderID = entry.ProviderID
				usage.ModelAlias = entry.ModelAlias
				usage.UpstreamModel = entry.UpstreamModel
				usage.Protocol = protocol
				usage.Endpoint = endpoint
				usage.Stream = false
				usage.StatusCode = entry.StatusCode
				usage.LatencyMS = time.Since(started).Milliseconds()
				usage.CreatedAt = time.Now().UTC()
				usage.CostMicroCents = 0
				if usage.Currency == "" {
					usage.Currency = entry.Currency
				}
				if usage.Currency == "" {
					usage.Currency = domain.DefaultBillingCurrency
				}
				if err := handler.store.InsertUsage(context.WithoutCancel(r.Context()), usage); err != nil {
					log.Printf("insert cached usage request_id=%s err=%v", requestID, err)
				}
				if err := handler.store.TouchAPIKey(context.WithoutCancel(r.Context()), subject.APIKeyID); err != nil {
					log.Printf("touch api key request_id=%s err=%v", requestID, err)
				}
				handler.logRequest(gatewayLogInput{RequestID: requestID, Subject: subject, Protocol: protocol, Endpoint: endpoint, ModelAlias: entry.ModelAlias, ProviderID: entry.ProviderID, UpstreamModel: entry.UpstreamModel, Stream: false, StatusCode: entry.StatusCode, Started: started, Usage: usage, Message: "response cache hit"})
				return
			}
		}
	}
	candidates, err := handler.resolveCandidates(r.Context(), protocol, request)
	if err != nil {
		status := http.StatusInternalServerError
		code := "store_error"
		message := err.Error()
		var reqErr *requestError
		if errors.As(err, &reqErr) {
			status = reqErr.Status
			code = reqErr.Code
			message = reqErr.Message
		}
		writeProtocolError(w, status, code, message, protocol, nil)
		handler.logRequest(gatewayLogInput{RequestID: requestID, Subject: subject, Protocol: protocol, Endpoint: endpoint, ModelAlias: request.Model, StatusCode: status, ErrorCode: code, Started: started, Stream: request.Stream, Message: message})
		return
	}
	primaryCandidate := candidates[0]
	estimatedUsage := estimateRequestUsage(body)
	estimatedCostMicroCents := int64(0)
	if endpointBillsUsage(endpoint) {
		estimatedCostMicroCents = estimateUsageCost(primaryCandidate.Pricing, estimatedUsage)
	}
	reservation, err := handler.store.ReserveQuota(r.Context(), domain.QuotaRequest{
		RequestID:               requestID,
		APIKeyID:                subject.APIKeyID,
		UserID:                  subject.UserID,
		ModelAlias:              request.Model,
		RPMLimit:                subject.RPMLimit,
		TPMLimit:                subject.TPMLimit,
		ConcurrentLimit:         subject.ConcurrentLimit,
		DailyBudgetCents:        subject.DailyBudgetCents,
		MonthlyBudgetCents:      subject.MonthlyBudgetCents,
		EstimatedTokens:         estimatedUsage.TotalTokens,
		EstimatedCostMicroCents: estimatedCostMicroCents,
		Now:                     time.Now().UTC(),
	})
	if err != nil {
		statusCode := http.StatusInternalServerError
		code := "quota_check_failed"
		if errors.Is(err, storage.ErrQuotaExceeded) {
			statusCode = http.StatusTooManyRequests
			code = "quota_exceeded"
		}
		writeProtocolError(w, statusCode, code, err.Error(), protocol, nil)
		handler.logRequest(gatewayLogInput{RequestID: requestID, Subject: subject, Protocol: protocol, Endpoint: endpoint, ModelAlias: primaryCandidate.Route.PublicName, ProviderID: primaryCandidate.Provider.ID, UpstreamModel: primaryCandidate.Route.UpstreamModel, ProxyProfileID: primaryCandidate.Profile.ID, Stream: request.Stream, StatusCode: statusCode, ErrorCode: code, Started: started, Message: err.Error()})
		return
	}
	var response *http.Response
	selectedCandidate := primaryCandidate
	var lastErr error
	attempts := make([]routerMetadataAttempt, 0, len(candidates))
	responseMode := upstreamResponseModeDirect
	for index, candidate := range candidates {
		selectedCandidate = candidate
		responseMode = upstreamResponseModeDirect
		client, err := handler.transports.HTTPClient(r.Context(), candidate.Provider, candidate.Profile, request.Stream)
		if err != nil {
			lastErr = err
			attempts = append(attempts, routerMetadataAttempt{Index: index + 1, Provider: candidate.Provider.ID, Model: candidate.Route.UpstreamModel, Status: "failed", FailureClass: "network_error"})
			if index < len(candidates)-1 {
				continue
			}
			break
		}
		rewritten, err := rewriteModel(body, candidate.Route.UpstreamModel)
		if err != nil {
			writeProtocolError(w, http.StatusBadRequest, "invalid_json", err.Error(), protocol, nil)
			return
		}
		upstream, err := handler.buildUpstreamRequest(r.Context(), r, endpoint, candidate.Provider, protocol, rewritten)
		if err != nil {
			writeProtocolError(w, http.StatusInternalServerError, "build_upstream_failed", err.Error(), protocol, nil)
			return
		}
		response, err = client.Do(upstream)
		if err != nil {
			lastErr = err
			attempts = append(attempts, routerMetadataAttempt{Index: index + 1, Provider: candidate.Provider.ID, Model: candidate.Route.UpstreamModel, Status: "failed", FailureClass: "network_error"})
			if index < len(candidates)-1 {
				continue
			}
			break
		}
		if shouldFallbackResponsesToChat(endpoint, response.StatusCode) {
			_ = response.Body.Close()
			fallbackResponse, fallbackErr := handler.forwardResponsesViaChatCompletions(r.Context(), r, client, candidate.Provider, rewritten)
			if fallbackErr != nil {
				lastErr = fallbackErr
				attempts = append(attempts, routerMetadataAttempt{Index: index + 1, Provider: candidate.Provider.ID, Model: candidate.Route.UpstreamModel, Status: "failed", FailureClass: "responses_fallback_failed"})
				if index < len(candidates)-1 {
					response = nil
					continue
				}
				response = nil
				break
			}
			response = fallbackResponse
			responseMode = upstreamResponseModeResponsesChatFallback
		}
		if retryableUpstreamStatus(response.StatusCode) && index < len(candidates)-1 {
			attempts = append(attempts, routerMetadataAttempt{Index: index + 1, Provider: candidate.Provider.ID, Model: candidate.Route.UpstreamModel, Status: "failed", StatusCode: response.StatusCode, FailureClass: failureClassFromStatus(response.StatusCode)})
			_ = response.Body.Close()
			response = nil
			continue
		}
		attemptStatus := "success"
		failureClass := ""
		if response.StatusCode >= 400 {
			attemptStatus = "failed"
			failureClass = failureClassFromStatus(response.StatusCode)
		}
		attempts = append(attempts, routerMetadataAttempt{Index: index + 1, Provider: candidate.Provider.ID, Model: candidate.Route.UpstreamModel, Status: attemptStatus, StatusCode: response.StatusCode, FailureClass: failureClass})
		break
	}
	metadata := buildRouterMetadata(request, candidates, selectedCandidate, attempts)
	if response == nil {
		usage := domain.UsageRecord{RequestID: requestID, UserID: subject.UserID, APIKeyID: subject.APIKeyID, ProviderID: selectedCandidate.Provider.ID, ModelAlias: selectedCandidate.Route.PublicName, UpstreamModel: selectedCandidate.Route.UpstreamModel, Protocol: protocol, Endpoint: endpoint, Currency: domain.DefaultBillingCurrency, Stream: request.Stream, StatusCode: http.StatusBadGateway, LatencyMS: time.Since(started).Milliseconds(), CreatedAt: time.Now().UTC()}
		if settleErr := handler.store.SettleQuota(context.WithoutCancel(r.Context()), reservation, usage); settleErr != nil {
			log.Printf("settle quota request_id=%s err=%v", requestID, settleErr)
		}
		if insertErr := handler.store.InsertUsage(context.WithoutCancel(r.Context()), usage); insertErr != nil {
			log.Printf("insert failed usage request_id=%s err=%v", requestID, insertErr)
		}
		message := "upstream request failed"
		if lastErr != nil {
			message = lastErr.Error()
		}
		writeProtocolError(w, http.StatusBadGateway, "upstream_error", message, protocol, metadataErrorPayload(includeRouterMetadata, metadata))
		handler.logRequest(gatewayLogInput{RequestID: requestID, Subject: subject, Protocol: protocol, Endpoint: endpoint, ModelAlias: selectedCandidate.Route.PublicName, ProviderID: selectedCandidate.Provider.ID, UpstreamModel: selectedCandidate.Route.UpstreamModel, ProxyProfileID: selectedCandidate.Profile.ID, Stream: request.Stream, StatusCode: http.StatusBadGateway, ErrorCode: "upstream_error", Started: started, Usage: usage, Message: message})
		return
	}
	defer response.Body.Close()
	responseMetadata := (*routerMetadata)(nil)
	if includeRouterMetadata {
		responseMetadata = &metadata
	}
	var usage domain.UsageRecord
	var copyErr error
	if request.Stream {
		if responseMode == upstreamResponseModeResponsesChatFallback {
			usage, copyErr = copyChatCompletionsStreamAsResponses(w, response, responseMetadata)
		} else {
			usage, copyErr = copyCompatibleResponse(w, response, protocol, responseMetadata)
		}
	} else {
		responseBody, readErr := io.ReadAll(response.Body)
		if readErr != nil {
			copyErr = readErr
		} else {
			usage = parseUsageFromJSON(responseBody, protocol)
			writtenBody := responseBody
			if responseMode == upstreamResponseModeResponsesChatFallback {
				convertedBody, convertedUsage, convertErr := chatCompletionResponseToResponses(responseBody, response.StatusCode)
				if convertErr != nil {
					copyErr = convertErr
				} else {
					writtenBody = convertedBody
					mergeUsage(&usage, convertedUsage)
				}
			}
			if responseMetadata != nil {
				writtenBody = injectRouterMetadata(writtenBody, *responseMetadata)
			}
			if copyErr == nil {
				copyResponseHeaders(w.Header(), response.Header)
				if responseMode == upstreamResponseModeResponsesChatFallback {
					w.Header().Set("Content-Type", "application/json")
				}
				if cacheControl.Enabled {
					setResponseCacheHeaders(w.Header(), "MISS", 0, cacheControl.TTLSeconds)
				}
				w.WriteHeader(response.StatusCode)
				if _, writeErr := w.Write(writtenBody); writeErr != nil {
					copyErr = writeErr
				} else if cacheControl.Enabled && response.StatusCode < http.StatusBadRequest {
					handler.storeResponseCache(cacheKey, responseCacheEntry{
						StatusCode:    response.StatusCode,
						Headers:       response.Header.Clone(),
						Body:          writtenBody,
						CreatedAt:     time.Now().UTC(),
						TTLSeconds:    cacheControl.TTLSeconds,
						ProviderID:    selectedCandidate.Provider.ID,
						ModelAlias:    selectedCandidate.Route.PublicName,
						UpstreamModel: selectedCandidate.Route.UpstreamModel,
						Currency:      selectedCandidate.Pricing.Currency,
						Protocol:      protocol,
						Endpoint:      endpoint,
					})
				}
			}
		}
	}
	if copyErr != nil {
		log.Printf("copy upstream response request_id=%s err=%v", requestID, copyErr)
	}
	latencyMS := time.Since(started).Milliseconds()
	usage.RequestID = requestID
	usage.UserID = subject.UserID
	usage.APIKeyID = subject.APIKeyID
	usage.ProviderID = selectedCandidate.Provider.ID
	usage.ModelAlias = selectedCandidate.Route.PublicName
	usage.UpstreamModel = selectedCandidate.Route.UpstreamModel
	usage.Protocol = protocol
	usage.Endpoint = endpoint
	usage.Stream = request.Stream
	usage.StatusCode = response.StatusCode
	usage.LatencyMS = latencyMS
	usage.CreatedAt = time.Now().UTC()
	if usage.Currency == "" {
		if selectedCandidate.Pricing.Currency != "" {
			usage.Currency = selectedCandidate.Pricing.Currency
		} else {
			usage.Currency = domain.DefaultBillingCurrency
		}
	}
	if endpointBillsUsage(endpoint) && usage.CostMicroCents == 0 {
		usage.CostMicroCents = calculateUsageCost(selectedCandidate.Pricing, usage)
	}
	if usage.Currency == "" {
		usage.Currency = domain.DefaultBillingCurrency
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens + usage.CacheCreationTokens + usage.CacheReadTokens
	}
	if usage.TotalTokens == 0 && response.StatusCode < 400 {
		usage.Estimated = true
	}
	if err := handler.store.SettleQuota(context.WithoutCancel(r.Context()), reservation, usage); err != nil {
		log.Printf("settle quota request_id=%s err=%v", requestID, err)
	}
	if err := handler.store.InsertUsage(context.WithoutCancel(r.Context()), usage); err != nil {
		log.Printf("insert usage request_id=%s err=%v", requestID, err)
	}
	if err := handler.store.TouchAPIKey(context.WithoutCancel(r.Context()), subject.APIKeyID); err != nil {
		log.Printf("touch api key request_id=%s err=%v", requestID, err)
	}
	handler.logRequest(gatewayLogInput{RequestID: requestID, Subject: subject, Protocol: protocol, Endpoint: endpoint, ModelAlias: selectedCandidate.Route.PublicName, ProviderID: selectedCandidate.Provider.ID, UpstreamModel: selectedCandidate.Route.UpstreamModel, ProxyProfileID: selectedCandidate.Profile.ID, Stream: request.Stream, StatusCode: response.StatusCode, Started: started, Usage: usage})
}

func (handler *Handler) keyInfo(w http.ResponseWriter, r *http.Request) {
	keyValue, err := auth.ExtractAPIKey(r)
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, "missing_api_key", "API key is required")
		return
	}
	apiKey, err := handler.store.FindAPIKeyByHash(r.Context(), auth.HashAPIKey(keyValue))
	if err != nil || !auth.APIKeyActive(apiKey, time.Now().UTC()) {
		writeJSONError(w, http.StatusUnauthorized, "invalid_api_key", "API key is invalid or disabled")
		return
	}
	summary, err := handler.store.SummarizeAPIKeyUsage(r.Context(), apiKey.ID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	response := struct {
		Data struct {
			ID                 string                    `json:"id"`
			Name               string                    `json:"name"`
			Status             string                    `json:"status"`
			CreatedAt          time.Time                 `json:"created_at"`
			LastUsedAt         *time.Time                `json:"last_used_at,omitempty"`
			IncludeByokInLimit bool                      `json:"include_byok_in_limit"`
			ByokUsage          domain.UsageTotals        `json:"byok_usage"`
			Usage              domain.APIKeyUsageSummary `json:"usage"`
			Limits             struct {
				RPM                    int   `json:"rpm"`
				TPM                    int   `json:"tpm"`
				Concurrent             int   `json:"concurrent"`
				DailyBudgetCents       int64 `json:"daily_budget_cents"`
				MonthlyBudgetCents     int64 `json:"monthly_budget_cents"`
				DailyBudgetRemaining   int64 `json:"daily_budget_remaining_cents"`
				MonthlyBudgetRemaining int64 `json:"monthly_budget_remaining_cents"`
			} `json:"limits"`
		} `json:"data"`
	}{}
	response.Data.ID = apiKey.ID
	response.Data.Name = apiKey.Name
	response.Data.Status = apiKey.Status
	response.Data.CreatedAt = apiKey.CreatedAt
	response.Data.LastUsedAt = apiKey.LastUsedAt
	response.Data.IncludeByokInLimit = false
	response.Data.ByokUsage = domain.UsageTotals{}
	response.Data.Usage = summary
	response.Data.Limits.RPM = apiKey.RPMLimit
	response.Data.Limits.TPM = apiKey.TPMLimit
	response.Data.Limits.Concurrent = apiKey.ConcurrentLimit
	response.Data.Limits.DailyBudgetCents = apiKey.DailyBudgetCents
	response.Data.Limits.MonthlyBudgetCents = apiKey.MonthlyBudgetCents
	response.Data.Limits.DailyBudgetRemaining = remainingBudget(apiKey.DailyBudgetCents, summary.Daily.CostMicroCents)
	response.Data.Limits.MonthlyBudgetRemaining = remainingBudget(apiKey.MonthlyBudgetCents, summary.Monthly.CostMicroCents)
	writeJSON(w, http.StatusOK, response)
}

func (handler *Handler) generation(w http.ResponseWriter, r *http.Request) {
	subject, ok := handler.authenticate(w, r, "", "")
	if !ok {
		return
	}
	requestID := strings.TrimSpace(r.URL.Query().Get("id"))
	if requestID == "" {
		writeJSONError(w, http.StatusBadRequest, "invalid_request_error", "generation id is required")
		return
	}
	usage, err := handler.store.GetUsageByRequestID(r.Context(), requestID)
	if errors.Is(err, storage.ErrNotFound) {
		writeJSONError(w, http.StatusNotFound, "generation_not_found", "generation not found")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	if usage.APIKeyID != "" && subject.APIKeyID != "" && usage.APIKeyID != subject.APIKeyID {
		writeJSONError(w, http.StatusForbidden, "not_allowed", "generation does not belong to this API key")
		return
	}
	response := struct {
		Data struct {
			ID                  string    `json:"id"`
			ProviderID          string    `json:"provider_id"`
			ModelAlias          string    `json:"model_alias"`
			UpstreamModel       string    `json:"upstream_model"`
			Protocol            string    `json:"protocol"`
			Endpoint            string    `json:"endpoint"`
			InputTokens         int       `json:"input_tokens"`
			OutputTokens        int       `json:"output_tokens"`
			CacheReadTokens     int       `json:"cache_read_tokens"`
			CacheCreationTokens int       `json:"cache_creation_tokens"`
			TotalTokens         int       `json:"total_tokens"`
			CostMicroCents      int64     `json:"cost_micro_cents"`
			Currency            string    `json:"currency"`
			StatusCode          int       `json:"status_code"`
			LatencyMS           int64     `json:"latency_ms"`
			Stream              bool      `json:"stream"`
			CreatedAt           time.Time `json:"created_at"`
		} `json:"data"`
	}{}
	response.Data.ID = usage.RequestID
	response.Data.ProviderID = usage.ProviderID
	response.Data.ModelAlias = usage.ModelAlias
	response.Data.UpstreamModel = usage.UpstreamModel
	response.Data.Protocol = usage.Protocol
	response.Data.Endpoint = usage.Endpoint
	response.Data.InputTokens = usage.InputTokens
	response.Data.OutputTokens = usage.OutputTokens
	response.Data.CacheReadTokens = usage.CacheReadTokens
	response.Data.CacheCreationTokens = usage.CacheCreationTokens
	response.Data.TotalTokens = usage.TotalTokens
	response.Data.CostMicroCents = usage.CostMicroCents
	response.Data.Currency = usage.Currency
	response.Data.StatusCode = usage.StatusCode
	response.Data.LatencyMS = usage.LatencyMS
	response.Data.Stream = usage.Stream
	response.Data.CreatedAt = usage.CreatedAt
	writeJSON(w, http.StatusOK, response)
}

func remainingBudget(limitCents int64, costMicroCents int64) int64 {
	if limitCents <= 0 {
		return 0
	}
	usedCents := costMicroCents / 1000000
	remaining := limitCents - usedCents
	if remaining < 0 {
		return 0
	}
	return remaining
}

func parseCompatibleRequest(body []byte) (compatibleRequest, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return compatibleRequest{}, err
	}
	model, _ := payload["model"].(string)
	if strings.TrimSpace(model) == "" {
		return compatibleRequest{}, &requestError{Status: http.StatusBadRequest, Code: "invalid_json", Message: "model is required"}
	}
	stream, _ := payload["stream"].(bool)
	fallbackModels, err := parseStringList(payload["models"])
	if err != nil {
		return compatibleRequest{}, &requestError{Status: http.StatusBadRequest, Code: "invalid_request_error", Message: err.Error()}
	}
	filteredFallbacks := make([]string, 0, len(fallbackModels))
	seen := map[string]struct{}{strings.TrimSpace(model): {}}
	for _, fallbackModel := range fallbackModels {
		trimmed := strings.TrimSpace(fallbackModel)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		filteredFallbacks = append(filteredFallbacks, trimmed)
	}
	provider, err := parseProviderPreferences(payload["provider"])
	if err != nil {
		return compatibleRequest{}, err
	}
	return compatibleRequest{Model: strings.TrimSpace(model), FallbackModels: filteredFallbacks, Stream: stream, Payload: payload, RequiredParameters: requestedParameterNames(payload), Provider: provider}, nil
}

func parseProviderPreferences(value any) (providerPreferences, error) {
	if value == nil {
		return providerPreferences{}, nil
	}
	payload, ok := value.(map[string]any)
	if !ok {
		return providerPreferences{}, &requestError{Status: http.StatusBadRequest, Code: "invalid_request_error", Message: "provider must be an object"}
	}
	var prefs providerPreferences
	for key, raw := range payload {
		switch key {
		case "order":
			values, err := parseStringList(raw)
			if err != nil {
				return providerPreferences{}, &requestError{Status: http.StatusBadRequest, Code: "invalid_request_error", Message: err.Error()}
			}
			prefs.Order = values
		case "only":
			values, err := parseStringList(raw)
			if err != nil {
				return providerPreferences{}, &requestError{Status: http.StatusBadRequest, Code: "invalid_request_error", Message: err.Error()}
			}
			prefs.Only = values
		case "ignore":
			values, err := parseStringList(raw)
			if err != nil {
				return providerPreferences{}, &requestError{Status: http.StatusBadRequest, Code: "invalid_request_error", Message: err.Error()}
			}
			prefs.Ignore = values
		case "allow_fallbacks":
			allowed, ok := raw.(bool)
			if !ok {
				return providerPreferences{}, &requestError{Status: http.StatusBadRequest, Code: "invalid_request_error", Message: "provider.allow_fallbacks must be a boolean"}
			}
			prefs.AllowFallbacks = allowed
			prefs.HasAllowFallbacks = true
		case "require_parameters":
			requireParameters, ok := raw.(bool)
			if !ok {
				return providerPreferences{}, &requestError{Status: http.StatusBadRequest, Code: "invalid_request_error", Message: "provider.require_parameters must be a boolean"}
			}
			prefs.RequireParameters = requireParameters
		case "sort":
			sortValue, ok := raw.(string)
			if !ok {
				return providerPreferences{}, &requestError{Status: http.StatusBadRequest, Code: "invalid_request_error", Message: "provider.sort must be a string"}
			}
			sortValue = strings.ToLower(strings.TrimSpace(sortValue))
			switch sortValue {
			case "", "price", "priority", "latency", "throughput":
				prefs.Sort = sortValue
			default:
				return providerPreferences{}, &requestError{Status: http.StatusBadRequest, Code: "invalid_request_error", Message: fmt.Sprintf("provider.sort %q is not supported", sortValue)}
			}
		case "max_price":
			priceLimits, err := parseProviderMaxPrice(raw)
			if err != nil {
				return providerPreferences{}, err
			}
			prefs.MaxPrice = priceLimits
			prefs.HasMaxPrice = true
		case "data_collection":
			policy, ok := raw.(string)
			if !ok {
				return providerPreferences{}, &requestError{Status: http.StatusBadRequest, Code: "invalid_request_error", Message: "provider.data_collection must be a string"}
			}
			policy = strings.ToLower(strings.TrimSpace(policy))
			prefs.DataCollection = policy
			prefs.HasDataCollection = true
			if policy == "deny" {
				return providerPreferences{}, &requestError{Status: http.StatusBadRequest, Code: "invalid_request_error", Message: "provider.data_collection=deny is not supported yet"}
			}
			if policy != "" && policy != "allow" {
				return providerPreferences{}, &requestError{Status: http.StatusBadRequest, Code: "invalid_request_error", Message: fmt.Sprintf("provider.data_collection %q is not supported", policy)}
			}
		case "zdr":
			zdr, ok := raw.(bool)
			if !ok {
				return providerPreferences{}, &requestError{Status: http.StatusBadRequest, Code: "invalid_request_error", Message: "provider.zdr must be a boolean"}
			}
			prefs.ZDR = zdr
			prefs.HasZDR = true
			if zdr {
				return providerPreferences{}, &requestError{Status: http.StatusBadRequest, Code: "invalid_request_error", Message: "provider.zdr=true is not supported yet"}
			}
		default:
			return providerPreferences{}, &requestError{Status: http.StatusBadRequest, Code: "invalid_request_error", Message: fmt.Sprintf("provider.%s is not supported yet", key)}
		}
	}
	return prefs, nil
}

func parseProviderMaxPrice(value any) (providerPriceLimits, error) {
	payload, ok := value.(map[string]any)
	if !ok {
		return providerPriceLimits{}, &requestError{Status: http.StatusBadRequest, Code: "invalid_request_error", Message: "provider.max_price must be an object"}
	}
	limits := providerPriceLimits{}
	for key, raw := range payload {
		parsed, err := numericValue(raw)
		if err != nil {
			return providerPriceLimits{}, &requestError{Status: http.StatusBadRequest, Code: "invalid_request_error", Message: fmt.Sprintf("provider.max_price.%s must be numeric", key)}
		}
		switch key {
		case "prompt":
			limits.Prompt = parsed
		case "completion":
			limits.Completion = parsed
		default:
			return providerPriceLimits{}, &requestError{Status: http.StatusBadRequest, Code: "invalid_request_error", Message: fmt.Sprintf("provider.max_price.%s is not supported", key)}
		}
	}
	return limits, nil
}

func parseStringList(value any) ([]string, error) {
	if value == nil {
		return nil, nil
	}
	items, ok := value.([]any)
	if !ok {
		return nil, errors.New("expected an array of strings")
	}
	result := make([]string, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			return nil, errors.New("expected an array of strings")
		}
		trimmed := strings.TrimSpace(text)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	return result, nil
}

func numericValue(value any) (float64, error) {
	switch typed := value.(type) {
	case float64:
		return typed, nil
	case string:
		ratio, ok := new(big.Rat).SetString(strings.TrimSpace(typed))
		if !ok {
			return 0, errors.New("invalid number")
		}
		floatValue, _ := ratio.Float64()
		return floatValue, nil
	case json.Number:
		return typed.Float64()
	default:
		return 0, errors.New("invalid number")
	}
}

func requestedParameterNames(payload map[string]any) []string {
	ignored := map[string]struct{}{
		"model":        {},
		"models":       {},
		"messages":     {},
		"prompt":       {},
		"suffix":       {},
		"input":        {},
		"instructions": {},
		"system":       {},
		"provider":     {},
		"route":        {},
	}
	parameters := make([]string, 0, len(payload))
	for key, value := range payload {
		if _, ok := ignored[key]; ok || value == nil {
			continue
		}
		parameters = append(parameters, key)
	}
	sort.Strings(parameters)
	return parameters
}

func (handler *Handler) authorizeAdditionalModels(w http.ResponseWriter, subject domain.Subject, protocol string, models []string) bool {
	for _, model := range models {
		if auth.Allows(subject, protocol, model) {
			continue
		}
		writeProtocolError(w, http.StatusForbidden, "not_allowed", "API key does not allow this protocol or model", protocol, nil)
		return false
	}
	return true
}

func (handler *Handler) resolveCandidates(ctx context.Context, protocol string, request compatibleRequest) ([]routeCandidate, error) {
	aliases := append([]string{request.Model}, request.FallbackModels...)
	candidates := make([]routeCandidate, 0, len(aliases))
	for index, alias := range aliases {
		candidate, err := handler.resolveCandidate(ctx, alias)
		if err != nil {
			if index == 0 {
				return nil, &requestError{Status: http.StatusNotFound, Code: "model_not_found", Message: err.Error()}
			}
			continue
		}
		if !domain.SupportsProtocol(domain.ProviderSupportedProtocols(candidate.Provider), protocol) || !domain.SupportsProtocol(domain.RouteAllowedProtocols(candidate.Route, candidate.Provider), protocol) {
			if index == 0 {
				return nil, &requestError{Status: http.StatusBadRequest, Code: "protocol_mismatch", Message: "model route does not allow this compatible protocol"}
			}
			continue
		}
		candidate.ModelIndex = index
		candidates = append(candidates, candidate)
	}
	if len(candidates) == 0 {
		return nil, &requestError{Status: http.StatusNotFound, Code: "model_not_found", Message: fmt.Sprintf("model %s not found", request.Model)}
	}
	if len(request.Provider.Only) > 0 {
		allowedProviders := sliceToSet(request.Provider.Only)
		candidates = filterCandidates(candidates, func(candidate routeCandidate) bool {
			_, ok := allowedProviders[candidate.Provider.ID]
			return ok
		})
	}
	if len(request.Provider.Ignore) > 0 {
		ignoredProviders := sliceToSet(request.Provider.Ignore)
		candidates = filterCandidates(candidates, func(candidate routeCandidate) bool {
			_, ok := ignoredProviders[candidate.Provider.ID]
			return !ok
		})
	}
	if request.Provider.RequireParameters {
		candidates = filterCandidates(candidates, func(candidate routeCandidate) bool {
			return candidateSupportsParameters(candidate, request.RequiredParameters)
		})
	}
	if request.Provider.HasMaxPrice {
		candidates = filterCandidates(candidates, func(candidate routeCandidate) bool {
			return candidateMatchesPriceLimits(candidate, request.Provider.MaxPrice)
		})
	}
	if len(candidates) == 0 {
		return nil, &requestError{Status: http.StatusBadRequest, Code: "invalid_provider_preferences", Message: "no routes matched provider preferences"}
	}
	candidates = sortCandidates(candidates, request.Provider)
	if len(candidates) == 0 {
		return nil, &requestError{Status: http.StatusBadRequest, Code: "invalid_provider_preferences", Message: "no routes matched provider preferences"}
	}
	return candidates, nil
}

func (handler *Handler) resolveCandidate(ctx context.Context, model string) (routeCandidate, error) {
	route, provider, profile, err := handler.resolveRoute(ctx, model)
	if err != nil {
		return routeCandidate{}, err
	}
	pricingRule, err := handler.pricingRuleForRoute(ctx, route)
	if err != nil {
		return routeCandidate{}, err
	}
	return routeCandidate{Route: route, Provider: provider, Profile: profile, Pricing: pricingRule}, nil
}

func sliceToSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[strings.TrimSpace(value)] = struct{}{}
	}
	return result
}

func filterCandidates(candidates []routeCandidate, allow func(routeCandidate) bool) []routeCandidate {
	filtered := make([]routeCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if allow(candidate) {
			filtered = append(filtered, candidate)
		}
	}
	return filtered
}

func candidateSupportsParameters(candidate routeCandidate, requested []string) bool {
	supported := sliceToSet(supportedParameters(candidate.Route, candidate.Provider))
	for _, parameter := range requested {
		if _, ok := supported[parameter]; ok {
			continue
		}
		return false
	}
	return true
}

func candidateMatchesPriceLimits(candidate routeCandidate, limits providerPriceLimits) bool {
	if limits.Prompt > 0 && pricePerToken(candidate.Pricing.InputPricePerMillionTokens) > limits.Prompt {
		return false
	}
	if limits.Completion > 0 && pricePerToken(candidate.Pricing.OutputPricePerMillionTokens) > limits.Completion {
		return false
	}
	return true
}

func pricePerToken(value int64) float64 {
	if value <= 0 {
		return 0
	}
	return float64(value) / 100000000000000
}

func sortCandidates(candidates []routeCandidate, prefs providerPreferences) []routeCandidate {
	allowFallbacks := true
	if prefs.HasAllowFallbacks {
		allowFallbacks = prefs.AllowFallbacks
	}
	if len(prefs.Order) > 0 {
		orderedProviders := sliceToSet(prefs.Order)
		if !allowFallbacks {
			candidates = filterCandidates(candidates, func(candidate routeCandidate) bool {
				_, ok := orderedProviders[candidate.Provider.ID]
				return ok
			})
		}
	}
	providerOrder := make(map[string]int, len(prefs.Order))
	for index, providerID := range prefs.Order {
		providerOrder[strings.TrimSpace(providerID)] = index
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		left := candidates[i]
		right := candidates[j]
		if len(providerOrder) > 0 {
			leftRank, leftOK := providerOrder[left.Provider.ID]
			rightRank, rightOK := providerOrder[right.Provider.ID]
			if !leftOK {
				leftRank = len(providerOrder)
			}
			if !rightOK {
				rightRank = len(providerOrder)
			}
			if leftRank != rightRank {
				return leftRank < rightRank
			}
		}
		if less, decided := compareCandidateSort(left, right, prefs.Sort); decided {
			return less
		}
		if left.ModelIndex != right.ModelIndex {
			return left.ModelIndex < right.ModelIndex
		}
		if left.Route.Priority != right.Route.Priority {
			return left.Route.Priority < right.Route.Priority
		}
		if left.Route.Weight != right.Route.Weight {
			return left.Route.Weight > right.Route.Weight
		}
		if left.Provider.ID != right.Provider.ID {
			return left.Provider.ID < right.Provider.ID
		}
		return left.Route.PublicName < right.Route.PublicName
	})
	return candidates
}

func compareCandidateSort(left, right routeCandidate, sortValue string) (bool, bool) {
	switch sortValue {
	case "price":
		leftPrice := left.Pricing.InputPricePerMillionTokens + left.Pricing.OutputPricePerMillionTokens
		rightPrice := right.Pricing.InputPricePerMillionTokens + right.Pricing.OutputPricePerMillionTokens
		if leftPrice != rightPrice {
			return leftPrice < rightPrice, true
		}
	case "priority", "latency":
		if left.Route.Priority != right.Route.Priority {
			return left.Route.Priority < right.Route.Priority, true
		}
	case "throughput":
		if left.Route.Weight != right.Route.Weight {
			return left.Route.Weight > right.Route.Weight, true
		}
	}
	return false, false
}

func retryableUpstreamStatus(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func (handler *Handler) authenticate(w http.ResponseWriter, r *http.Request, protocol, model string) (domain.Subject, bool) {
	key, err := auth.ExtractAPIKey(r)
	if err != nil {
		writeProtocolError(w, http.StatusUnauthorized, "missing_api_key", "API key is required", protocol, nil)
		return domain.Subject{}, false
	}
	apiKey, err := handler.store.FindAPIKeyByHash(r.Context(), auth.HashAPIKey(key))
	if err != nil || !auth.APIKeyActive(apiKey, time.Now().UTC()) {
		writeProtocolError(w, http.StatusUnauthorized, "invalid_api_key", "API key is invalid or disabled", protocol, nil)
		return domain.Subject{}, false
	}
	subject := auth.SubjectFromAPIKey(apiKey)
	if !auth.Allows(subject, protocol, model) {
		writeProtocolError(w, http.StatusForbidden, "not_allowed", "API key does not allow this protocol or model", protocol, nil)
		return domain.Subject{}, false
	}
	return subject, true
}

func (handler *Handler) resolveRoute(ctx context.Context, model string) (domain.ModelRoute, domain.Provider, domain.ProxyProfile, error) {
	route, err := handler.store.GetModelRoute(ctx, model)
	if err != nil {
		return domain.ModelRoute{}, domain.Provider{}, domain.ProxyProfile{}, err
	}
	provider, err := handler.store.GetProvider(ctx, route.ProviderID)
	if err != nil {
		return domain.ModelRoute{}, domain.Provider{}, domain.ProxyProfile{}, err
	}
	if provider.Status != "" && provider.Status != domain.StatusEnabled {
		return domain.ModelRoute{}, domain.Provider{}, domain.ProxyProfile{}, fmt.Errorf("provider %s is disabled", provider.ID)
	}
	profileID := route.ProxyProfileID
	if profileID == "" {
		profileID = provider.DefaultProxyID
	}
	if profileID == "" {
		profileID = "direct"
	}
	profile, err := handler.store.GetProxyProfile(ctx, profileID)
	if err != nil {
		profile = domain.ProxyProfile{ID: "direct", Name: "direct", Type: "direct", Status: domain.StatusEnabled, TimeoutSeconds: provider.TimeoutSeconds}
	}
	return route, provider, profile, nil
}

func (handler *Handler) buildUpstreamRequest(ctx context.Context, clientRequest *http.Request, endpoint string, provider domain.Provider, protocol string, body []byte) (*http.Request, error) {
	upstreamURL, err := joinUpstreamURL(domain.ProviderBaseURLForProtocol(provider, protocol), endpoint)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, clientRequest.Method, upstreamURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	copyRequestHeaders(request.Header, clientRequest.Header, protocol)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", clientRequest.Header.Get("Accept"))
	if request.Header.Get("Accept") == "" {
		request.Header.Set("Accept", "application/json")
	}
	request.Header.Del("Authorization")
	request.Header.Del("x-api-key")
	if domain.IsOpenRouterProvider(provider) {
		request.Header.Set("Authorization", "Bearer "+provider.MasterKey)
		request.Header.Set("HTTP-Referer", "https://github.com/zltl/aiyolo")
		request.Header.Set("X-Title", "aiyolo")
	} else if protocol == domain.ProtocolAnthropic {
		request.Header.Set("x-api-key", provider.MasterKey)
	} else {
		request.Header.Set("Authorization", "Bearer "+provider.MasterKey)
	}
	return request, nil
}

func copyRequestHeaders(dst, src http.Header, protocol string) {
	allowed := map[string]bool{
		"accept":                             true,
		"content-type":                       true,
		"user-agent":                         true,
		"x-openrouter-experimental-metadata": true,
		"openai-organization":                true,
		"openai-project":                     true,
		"anthropic-version":                  true,
	}
	for name, values := range src {
		lower := strings.ToLower(name)
		if allowed[lower] || (protocol == domain.ProtocolAnthropic && lower == "anthropic-beta") {
			for _, value := range values {
				dst.Add(name, value)
			}
		}
	}
}

func joinUpstreamURL(baseURL string, endpoint string) (string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	basePath := strings.TrimRight(parsed.Path, "/")
	endpointPath := endpoint
	if strings.HasSuffix(basePath, "/v1") && strings.HasPrefix(endpointPath, "/v1/") {
		endpointPath = strings.TrimPrefix(endpointPath, "/v1")
	}
	parsed.Path = path.Join(basePath, endpointPath)
	return parsed.String(), nil
}

func inspectModelAndStream(body []byte) (string, bool, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", false, err
	}
	model, _ := payload["model"].(string)
	stream, _ := payload["stream"].(bool)
	if strings.TrimSpace(model) == "" {
		return "", false, errors.New("model is required")
	}
	return model, stream, nil
}

func rewriteModel(body []byte, upstreamModel string) ([]byte, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	payload["model"] = upstreamModel
	return json.Marshal(payload)
}

func estimateRequestTokens(body []byte) int {
	return estimateRequestUsage(body).TotalTokens
}

func estimateRequestUsage(body []byte) domain.UsageRecord {
	estimated := len(body) / 4
	usage := domain.UsageRecord{Currency: domain.DefaultBillingCurrency, InputTokens: estimated}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err == nil {
		usage.OutputTokens = intNumber(payload["max_tokens"])
		if usage.OutputTokens == 0 {
			usage.OutputTokens = intNumber(payload["max_completion_tokens"])
		}
	}
	if usage.InputTokens < 1 {
		usage.InputTokens = 1
	}
	usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	if usage.TotalTokens < 1 {
		usage.TotalTokens = 1
	}
	return usage
}

func endpointBillsUsage(endpoint string) bool {
	return endpoint != "/v1/messages/count_tokens"
}

func estimateUsageCost(rule domain.PricingRule, usage domain.UsageRecord) int64 {
	return calculateUsageCost(rule, usage)
}

func calculateUsageCost(rule domain.PricingRule, usage domain.UsageRecord) int64 {
	return costForTokens(rule.InputPricePerMillionTokens, usage.InputTokens) +
		costForTokens(rule.OutputPricePerMillionTokens, usage.OutputTokens) +
		costForTokens(rule.CacheReadPricePerMillionTokens, usage.CacheReadTokens) +
		costForTokens(rule.CacheWritePricePerMillionTokens, usage.CacheCreationTokens)
}

func costForTokens(pricePerMillionTokens int64, tokens int) int64 {
	if pricePerMillionTokens <= 0 || tokens <= 0 {
		return 0
	}
	return (pricePerMillionTokens*int64(tokens) + 500000) / 1000000
}

func copyCompatibleResponse(w http.ResponseWriter, response *http.Response, protocol string, metadata *routerMetadata) (domain.UsageRecord, error) {
	contentType, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if contentType == "text/event-stream" {
		copyResponseHeaders(w.Header(), response.Header)
		w.WriteHeader(response.StatusCode)
		return copySSE(w, response.Body, protocol)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return domain.UsageRecord{}, err
	}
	usage := parseUsageFromJSON(body, protocol)
	if metadata != nil {
		body = injectRouterMetadata(body, *metadata)
	}
	copyResponseHeaders(w.Header(), response.Header)
	w.WriteHeader(response.StatusCode)
	if _, err := w.Write(body); err != nil {
		return domain.UsageRecord{}, err
	}
	return usage, nil
}

func parseResponseCacheControl(r *http.Request, stream bool) (responseCacheControl, error) {
	control := responseCacheControl{}
	if stream {
		return control, nil
	}
	control.Enabled = enabledHeaderValue(r.Header.Get("X-OpenRouter-Cache"))
	control.Clear = enabledHeaderValue(r.Header.Get("X-OpenRouter-Cache-Clear"))
	if !control.Enabled && !control.Clear {
		return control, nil
	}
	control.TTLSeconds = defaultResponseCacheTTLSeconds
	if value := strings.TrimSpace(r.Header.Get("X-OpenRouter-Cache-TTL")); value != "" {
		ttl, err := strconv.Atoi(value)
		if err != nil || ttl < 1 || ttl > 86400 {
			return responseCacheControl{}, &requestError{Status: http.StatusBadRequest, Code: "invalid_cache_ttl", Message: "X-OpenRouter-Cache-TTL must be between 1 and 86400"}
		}
		control.TTLSeconds = ttl
	}
	return control, nil
}

func responseCacheKey(apiKeyID string, request compatibleRequest, endpoint string, body []byte) string {
	hash := sha256.Sum256(body)
	return strings.Join([]string{apiKeyID, request.Model, endpoint, strconv.FormatBool(request.Stream), hex.EncodeToString(hash[:])}, ":")
}

func (handler *Handler) loadResponseCache(key string, now time.Time) (responseCacheEntry, bool) {
	handler.cacheMu.RLock()
	entry, ok := handler.cache[key]
	handler.cacheMu.RUnlock()
	if !ok {
		return responseCacheEntry{}, false
	}
	if entry.CreatedAt.Add(time.Duration(entry.TTLSeconds) * time.Second).Before(now) {
		handler.deleteResponseCache(key)
		return responseCacheEntry{}, false
	}
	return responseCacheEntry{
		StatusCode:    entry.StatusCode,
		Headers:       entry.Headers.Clone(),
		Body:          append([]byte(nil), entry.Body...),
		CreatedAt:     entry.CreatedAt,
		TTLSeconds:    entry.TTLSeconds,
		ProviderID:    entry.ProviderID,
		ModelAlias:    entry.ModelAlias,
		UpstreamModel: entry.UpstreamModel,
		Currency:      entry.Currency,
		Protocol:      entry.Protocol,
		Endpoint:      entry.Endpoint,
	}, true
}

func (handler *Handler) storeResponseCache(key string, entry responseCacheEntry) {
	handler.cacheMu.Lock()
	defer handler.cacheMu.Unlock()
	handler.cache[key] = responseCacheEntry{
		StatusCode:    entry.StatusCode,
		Headers:       entry.Headers.Clone(),
		Body:          append([]byte(nil), entry.Body...),
		CreatedAt:     entry.CreatedAt,
		TTLSeconds:    entry.TTLSeconds,
		ProviderID:    entry.ProviderID,
		ModelAlias:    entry.ModelAlias,
		UpstreamModel: entry.UpstreamModel,
		Currency:      entry.Currency,
		Protocol:      entry.Protocol,
		Endpoint:      entry.Endpoint,
	}
}

func (handler *Handler) deleteResponseCache(key string) {
	handler.cacheMu.Lock()
	defer handler.cacheMu.Unlock()
	delete(handler.cache, key)
}

func writeCachedResponse(w http.ResponseWriter, entry responseCacheEntry, protocol string) (domain.UsageRecord, error) {
	body := zeroUsageInCachedResponse(entry.Body, protocol)
	copyResponseHeaders(w.Header(), entry.Headers)
	setResponseCacheHeaders(w.Header(), "HIT", int(time.Since(entry.CreatedAt).Seconds()), entry.TTLSeconds)
	w.WriteHeader(entry.StatusCode)
	if _, err := w.Write(body); err != nil {
		return domain.UsageRecord{}, err
	}
	usage := parseUsageFromJSON(body, protocol)
	usage.CostMicroCents = 0
	return usage, nil
}

func setResponseCacheHeaders(headers http.Header, status string, ageSeconds int, ttlSeconds int) {
	headers.Set("X-OpenRouter-Cache-Status", status)
	headers.Set("X-OpenRouter-Cache-TTL", strconv.Itoa(ttlSeconds))
	if ageSeconds < 0 {
		ageSeconds = 0
	}
	headers.Set("X-OpenRouter-Cache-Age", strconv.Itoa(ageSeconds))
}

func zeroUsageInCachedResponse(body []byte, protocol string) []byte {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return body
	}
	usagePayload, ok := payload["usage"].(map[string]any)
	if !ok {
		return body
	}
	if protocol == domain.ProtocolAnthropic {
		usagePayload["input_tokens"] = 0
		usagePayload["output_tokens"] = 0
		usagePayload["cache_creation_input_tokens"] = 0
		usagePayload["cache_read_input_tokens"] = 0
	} else {
		usagePayload["prompt_tokens"] = 0
		usagePayload["completion_tokens"] = 0
		usagePayload["total_tokens"] = 0
		if details, ok := usagePayload["prompt_tokens_details"].(map[string]any); ok {
			details["cached_tokens"] = 0
			details["cache_write_tokens"] = 0
		}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return encoded
}

func enabledHeaderValue(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "enabled":
		return true
	default:
		return false
	}
}

func injectRouterMetadata(body []byte, metadata routerMetadata) []byte {
	if len(body) == 0 {
		return body
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return body
	}
	payload["aiyolo_metadata"] = metadata
	encoded, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return encoded
}

func copyResponseHeaders(dst, src http.Header) {
	for name, values := range src {
		lower := strings.ToLower(name)
		if lower == "content-length" || lower == "connection" || lower == "transfer-encoding" {
			continue
		}
		for _, value := range values {
			dst.Add(name, value)
		}
	}
}

func copySSE(w http.ResponseWriter, body io.Reader, protocol string) (domain.UsageRecord, error) {
	flusher, _ := w.(http.Flusher)
	reader := bufio.NewReader(body)
	var usage domain.UsageRecord
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			if _, writeErr := w.Write(line); writeErr != nil {
				return usage, writeErr
			}
			if flusher != nil {
				flusher.Flush()
			}
			mergeUsage(&usage, parseUsageFromSSELine(line, protocol))
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return usage, nil
			}
			return usage, err
		}
	}
}

func metadataRequested(r *http.Request) bool {
	value := strings.ToLower(strings.TrimSpace(r.Header.Get("X-OpenRouter-Experimental-Metadata")))
	switch value {
	case "enabled", "true", "1", "yes":
		return true
	default:
		return false
	}
}

func buildRouterMetadata(request compatibleRequest, candidates []routeCandidate, selected routeCandidate, attempts []routerMetadataAttempt) routerMetadata {
	metadata := routerMetadata{
		Requested:     request.Model,
		ResolvedModel: selected.Route.UpstreamModel,
		Strategy:      routingStrategyName(request, len(candidates)),
		Attempt:       len(attempts),
		IsByok:        false,
		Summary:       routingSummary(selected, attempts),
		Attempts:      append([]routerMetadataAttempt(nil), attempts...),
	}
	metadata.Endpoints.Total = len(candidates)
	metadata.Endpoints.Available = make([]routerMetadataEndpoint, 0, len(candidates))
	for _, candidate := range candidates {
		metadata.Endpoints.Available = append(metadata.Endpoints.Available, routerMetadataEndpoint{
			Provider: candidate.Provider.ID,
			Model:    candidate.Route.UpstreamModel,
			Selected: candidate.Provider.ID == selected.Provider.ID && candidate.Route.UpstreamModel == selected.Route.UpstreamModel,
		})
	}
	for index := len(attempts) - 1; index >= 0; index-- {
		if attempts[index].Status == "success" {
			metadata.Attempt = attempts[index].Index
			break
		}
	}
	if metadata.Attempt == 0 {
		metadata.Attempt = len(attempts)
	}
	return metadata
}

func routingStrategyName(request compatibleRequest, candidateCount int) string {
	if request.Provider.Sort != "" {
		switch request.Provider.Sort {
		case "price":
			return "cost_first"
		case "latency":
			return "latency_first"
		case "throughput":
			return "throughput_first"
		default:
			return request.Provider.Sort
		}
	}
	if len(request.FallbackModels) > 0 || candidateCount > 1 {
		return "fallback"
	}
	return "direct"
}

func routingSummary(selected routeCandidate, attempts []routerMetadataAttempt) string {
	if len(attempts) > 1 {
		return fmt.Sprintf("selected %s after %d attempts", selected.Provider.ID, len(attempts))
	}
	if len(attempts) == 1 && attempts[0].Status == "success" {
		return fmt.Sprintf("selected %s on first attempt", selected.Provider.ID)
	}
	return fmt.Sprintf("selected %s", selected.Provider.ID)
}

func failureClassFromStatus(status int) string {
	switch {
	case status == http.StatusTooManyRequests:
		return "rate_limited"
	case status >= 500:
		return "upstream_5xx"
	case status >= 400:
		return "upstream_4xx"
	default:
		return ""
	}
}

func metadataErrorPayload(enabled bool, metadata routerMetadata) map[string]any {
	if !enabled {
		return nil
	}
	return map[string]any{"aiyolo_metadata": metadata}
}

func parseUsageFromSSELine(line []byte, protocol string) domain.UsageRecord {
	trimmed := strings.TrimSpace(string(line))
	if !strings.HasPrefix(trimmed, "data:") {
		return domain.UsageRecord{}
	}
	data := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
	if data == "" || data == "[DONE]" {
		return domain.UsageRecord{}
	}
	return parseUsageFromJSON([]byte(data), protocol)
}

func parseUsageFromJSON(body []byte, protocol string) domain.UsageRecord {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return domain.UsageRecord{}
	}
	var usagePayload map[string]any
	if value, ok := payload["usage"].(map[string]any); ok {
		usagePayload = value
	}
	if protocol == domain.ProtocolAnthropic && usagePayload == nil {
		if message, ok := payload["message"].(map[string]any); ok {
			if value, ok := message["usage"].(map[string]any); ok {
				usagePayload = value
			}
		}
		if usagePayload == nil && payload["input_tokens"] != nil {
			usage := domain.UsageRecord{Currency: domain.DefaultBillingCurrency, InputTokens: intNumber(payload["input_tokens"])}
			usage.TotalTokens = usage.InputTokens
			return usage
		}
	}
	if usagePayload == nil {
		return domain.UsageRecord{}
	}
	usage := domain.UsageRecord{Currency: domain.DefaultBillingCurrency}
	if protocol == domain.ProtocolAnthropic {
		usage.InputTokens = intNumber(usagePayload["input_tokens"])
		usage.OutputTokens = intNumber(usagePayload["output_tokens"])
		usage.CacheCreationTokens = intNumber(usagePayload["cache_creation_input_tokens"])
		usage.CacheReadTokens = intNumber(usagePayload["cache_read_input_tokens"])
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens + usage.CacheCreationTokens + usage.CacheReadTokens
		return usage
	}
	usage.InputTokens = intNumber(usagePayload["prompt_tokens"])
	usage.OutputTokens = intNumber(usagePayload["completion_tokens"])
	if usage.InputTokens == 0 {
		usage.InputTokens = intNumber(usagePayload["input_tokens"])
	}
	if usage.OutputTokens == 0 {
		usage.OutputTokens = intNumber(usagePayload["output_tokens"])
	}
	usage.TotalTokens = intNumber(usagePayload["total_tokens"])
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	}
	return usage
}

func mergeUsage(target *domain.UsageRecord, next domain.UsageRecord) {
	if next.InputTokens > 0 {
		target.InputTokens = next.InputTokens
	}
	if next.OutputTokens > 0 {
		target.OutputTokens = next.OutputTokens
	}
	if next.CacheCreationTokens > 0 {
		target.CacheCreationTokens = next.CacheCreationTokens
	}
	if next.CacheReadTokens > 0 {
		target.CacheReadTokens = next.CacheReadTokens
	}
	if next.TotalTokens > 0 {
		target.TotalTokens = next.TotalTokens
	}
	if next.Currency != "" {
		target.Currency = next.Currency
	}
}

func (handler *Handler) pricingRuleForRoute(ctx context.Context, route domain.ModelRoute) (domain.PricingRule, error) {
	if strings.TrimSpace(route.PriceRuleID) == "" {
		return domain.PricingRule{}, nil
	}
	rule, err := handler.store.GetPricingRule(ctx, route.PriceRuleID)
	if errors.Is(err, storage.ErrNotFound) {
		return domain.PricingRule{}, nil
	}
	return rule, err
}

func modelPricingFromRule(rule domain.PricingRule) *modelListPricing {
	if rule.InputPricePerMillionTokens == 0 && rule.OutputPricePerMillionTokens == 0 && rule.CacheReadPricePerMillionTokens == 0 && rule.CacheWritePricePerMillionTokens == 0 {
		return nil
	}
	return &modelListPricing{
		Prompt:          microCentsPerMillionToPriceString(rule.InputPricePerMillionTokens),
		Completion:      microCentsPerMillionToPriceString(rule.OutputPricePerMillionTokens),
		InputCacheRead:  microCentsPerMillionToPriceString(rule.CacheReadPricePerMillionTokens),
		InputCacheWrite: microCentsPerMillionToPriceString(rule.CacheWritePricePerMillionTokens),
	}
}

func microCentsPerMillionToPriceString(value int64) string {
	if value <= 0 {
		return ""
	}
	ratio := big.NewRat(value, 100000000000000)
	formatted := ratio.FloatString(15)
	formatted = strings.TrimRight(formatted, "0")
	formatted = strings.TrimRight(formatted, ".")
	if formatted == "" {
		return "0"
	}
	return formatted
}

func supportedParameters(route domain.ModelRoute, provider domain.Provider) []string {
	protocols := domain.RouteAllowedProtocols(route, provider)
	if len(protocols) == 0 {
		protocols = []string{domain.ProtocolOpenAI}
	}
	result := make([]string, 0, 16)
	seen := make(map[string]struct{}, 16)
	appendUnique := func(values []string) {
		for _, value := range values {
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			result = append(result, value)
		}
	}
	for _, protocol := range protocols {
		switch protocol {
		case domain.ProtocolAnthropic:
			appendUnique([]string{"max_tokens", "system", "stream", "tools", "tool_choice", "stop_sequences", "temperature", "top_p", "top_k"})
		default:
			appendUnique([]string{"stream", "temperature", "top_p", "max_tokens", "max_completion_tokens", "stop", "tools", "tool_choice", "parallel_tool_calls", "response_format", "seed", "user", "service_tier"})
		}
	}
	return result
}

func intNumber(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	case json.Number:
		parsed, _ := typed.Int64()
		return int(parsed)
	default:
		return 0
	}
}

type gatewayLogInput struct {
	RequestID      string
	Subject        domain.Subject
	Protocol       string
	Endpoint       string
	ModelAlias     string
	ProviderID     string
	UpstreamModel  string
	ProxyProfileID string
	StatusCode     int
	ErrorCode      string
	Started        time.Time
	Stream         bool
	Usage          domain.UsageRecord
	Message        string
}

func (handler *Handler) logRequest(input gatewayLogInput) {
	latencyMS := time.Since(input.Started).Milliseconds()
	usage := input.Usage
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens + usage.CacheCreationTokens + usage.CacheReadTokens
	}
	log.Printf(
		"gateway request_id=%s user_id=%s api_key_id=%s protocol=%s endpoint=%s model=%s provider_id=%s upstream_model=%s proxy_profile_id=%s stream=%t status=%d latency_ms=%d input_tokens=%d output_tokens=%d total_tokens=%d cache_creation_tokens=%d cache_read_tokens=%d cost_micro_cents=%d estimated=%t error_code=%s message=%q",
		input.RequestID,
		input.Subject.UserID,
		input.Subject.APIKeyID,
		input.Protocol,
		input.Endpoint,
		input.ModelAlias,
		input.ProviderID,
		input.UpstreamModel,
		input.ProxyProfileID,
		input.Stream,
		input.StatusCode,
		latencyMS,
		usage.InputTokens,
		usage.OutputTokens,
		usage.TotalTokens,
		usage.CacheCreationTokens,
		usage.CacheReadTokens,
		usage.CostMicroCents,
		usage.Estimated,
		input.ErrorCode,
		input.Message,
	)
}
func requestID(r *http.Request) string {
	if value := strings.TrimSpace(r.Header.Get("x-request-id")); value != "" {
		return value
	}
	return newID("req")
}

func newID(prefix string) string {
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	}
	return prefix + "_" + hex.EncodeToString(raw)
}

func clientIP(r *http.Request) string {
	if forwarded := strings.TrimSpace(r.Header.Get("x-forwarded-for")); forwarded != "" {
		return strings.TrimSpace(strings.Split(forwarded, ",")[0])
	}
	return r.RemoteAddr
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeJSONError(w http.ResponseWriter, status int, code string, message string) {
	writeProtocolError(w, status, code, message, "", nil)
}

func writeProtocolError(w http.ResponseWriter, status int, code string, message string, protocol string, metadata map[string]any) {
	if protocol == domain.ProtocolAnthropic {
		errorPayload := map[string]any{"type": anthropicErrorType(status, code), "message": message}
		if len(metadata) > 0 {
			for key, value := range metadata {
				errorPayload[key] = value
			}
		}
		writeJSON(w, status, map[string]any{"type": "error", "error": errorPayload})
		return
	}
	openAIError := map[string]any{
		"message": message,
		"type":    openAIErrorType(status, code),
		"param":   nil,
		"code":    code,
	}
	if len(metadata) > 0 {
		for key, value := range metadata {
			openAIError[key] = value
		}
	}
	writeJSON(w, status, map[string]any{"error": openAIError})
}

func openAIErrorType(status int, code string) string {
	switch code {
	case "quota_exceeded":
		return "rate_limit_error"
	case "not_allowed":
		return "permission_error"
	case "upstream_error":
		return "api_error"
	default:
		if status == http.StatusTooManyRequests {
			return "rate_limit_error"
		}
		if status == http.StatusForbidden {
			return "permission_error"
		}
		if status >= 500 {
			return "api_error"
		}
		return "invalid_request_error"
	}
}

func anthropicErrorType(status int, code string) string {
	switch code {
	case "missing_api_key", "invalid_api_key":
		return "authentication_error"
	case "quota_exceeded":
		return "rate_limit_error"
	case "not_allowed":
		return "permission_error"
	case "generation_not_found":
		return "not_found_error"
	case "upstream_error":
		return "api_error"
	default:
		if status == http.StatusUnauthorized {
			return "authentication_error"
		}
		if status == http.StatusTooManyRequests {
			return "rate_limit_error"
		}
		if status == http.StatusForbidden {
			return "permission_error"
		}
		if status == http.StatusNotFound {
			return "not_found_error"
		}
		if status >= 500 {
			return "api_error"
		}
		return "invalid_request_error"
	}
}
