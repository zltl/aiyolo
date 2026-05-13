package gateway

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
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
}

func NewHandler(store storage.Store, transports *proxytransport.TransportFactory) *Handler {
	return &Handler{store: store, transports: transports}
}

func (handler *Handler) Routes() http.Handler {
	router := chi.NewRouter()
	router.Get("/models", handler.listModels)
	router.Post("/chat/completions", handler.forwardOpenAI("/v1/chat/completions"))
	router.Post("/completions", handler.forwardOpenAI("/v1/completions"))
	router.Post("/embeddings", handler.forwardOpenAI("/v1/embeddings"))
	router.Post("/responses", handler.forwardOpenAI("/v1/responses"))
	router.Post("/messages", handler.forwardAnthropic("/v1/messages"))
	router.Post("/messages/count_tokens", handler.forwardAnthropic("/v1/messages/count_tokens"))
	return router
}

func (handler *Handler) listModels(w http.ResponseWriter, r *http.Request) {
	if _, ok := handler.authenticate(w, r, domain.ProtocolOpenAI, ""); !ok {
		return
	}
	routes, err := handler.store.ListModelRoutes(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	type model struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Created int64  `json:"created"`
		OwnedBy string `json:"owned_by"`
	}
	response := struct {
		Object string  `json:"object"`
		Data   []model `json:"data"`
	}{Object: "list"}
	now := time.Now().Unix()
	for _, route := range routes {
		response.Data = append(response.Data, model{ID: route.PublicName, Object: "model", Created: now, OwnedBy: route.ProviderID})
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
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<20))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "read_body_failed", err.Error())
		return
	}
	_ = r.Body.Close()
	model, stream, err := inspectModelAndStream(body)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	subject, ok := handler.authenticate(w, r, protocol, model)
	if !ok {
		return
	}
	route, provider, profile, err := handler.resolveRoute(r.Context(), model)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "model_not_found", err.Error())
		handler.audit(r.Context(), auditInput{RequestID: requestID, Subject: subject, Protocol: protocol, Endpoint: endpoint, ModelAlias: model, StatusCode: http.StatusNotFound, ErrorCode: "model_not_found", Started: started, ClientIP: clientIP(r), UserAgent: r.UserAgent(), EventType: "api_call"})
		return
	}
	if route.Protocol != "" && route.Protocol != protocol {
		writeJSONError(w, http.StatusBadRequest, "protocol_mismatch", "model route does not allow this compatible protocol")
		return
	}
	rewritten, err := rewriteModel(body, route.UpstreamModel)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	upstream, err := handler.buildUpstreamRequest(r.Context(), r, endpoint, provider, protocol, rewritten)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "build_upstream_failed", err.Error())
		return
	}
	client, err := handler.transports.HTTPClient(r.Context(), provider, profile)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "proxy_error", err.Error())
		return
	}
	reservation, err := handler.store.ReserveQuota(r.Context(), domain.QuotaRequest{
		RequestID:               requestID,
		APIKeyID:                subject.APIKeyID,
		UserID:                  subject.UserID,
		ModelAlias:              model,
		RPMLimit:                subject.RPMLimit,
		TPMLimit:                subject.TPMLimit,
		ConcurrentLimit:         subject.ConcurrentLimit,
		DailyBudgetCents:        subject.DailyBudgetCents,
		MonthlyBudgetCents:      subject.MonthlyBudgetCents,
		EstimatedTokens:         estimateRequestTokens(body),
		EstimatedCostMicroCents: 0,
		Now:                     time.Now().UTC(),
	})
	if err != nil {
		statusCode := http.StatusInternalServerError
		code := "quota_check_failed"
		if errors.Is(err, storage.ErrQuotaExceeded) {
			statusCode = http.StatusTooManyRequests
			code = "quota_exceeded"
		}
		writeJSONError(w, statusCode, code, err.Error())
		handler.audit(context.WithoutCancel(r.Context()), auditInput{RequestID: requestID, Subject: subject, Protocol: protocol, Endpoint: endpoint, ModelAlias: model, ProviderID: provider.ID, UpstreamModel: route.UpstreamModel, ProxyProfileID: profile.ID, StatusCode: statusCode, ErrorCode: code, Started: started, ClientIP: clientIP(r), UserAgent: r.UserAgent(), EventType: "quota_check", Message: err.Error()})
		return
	}
	response, err := client.Do(upstream)
	if err != nil {
		usage := domain.UsageRecord{RequestID: requestID, UserID: subject.UserID, APIKeyID: subject.APIKeyID, ProviderID: provider.ID, ModelAlias: model, UpstreamModel: route.UpstreamModel, Protocol: protocol, Endpoint: endpoint, Currency: "USD", Stream: stream, StatusCode: http.StatusBadGateway, LatencyMS: time.Since(started).Milliseconds(), CreatedAt: time.Now().UTC()}
		if settleErr := handler.store.SettleQuota(context.WithoutCancel(r.Context()), reservation, usage); settleErr != nil {
			log.Printf("settle quota request_id=%s err=%v", requestID, settleErr)
		}
		if insertErr := handler.store.InsertUsage(context.WithoutCancel(r.Context()), usage); insertErr != nil {
			log.Printf("insert failed usage request_id=%s err=%v", requestID, insertErr)
		}
		writeJSONError(w, http.StatusBadGateway, "upstream_error", err.Error())
		handler.audit(context.WithoutCancel(r.Context()), auditInput{RequestID: requestID, Subject: subject, Protocol: protocol, Endpoint: endpoint, ModelAlias: model, ProviderID: provider.ID, UpstreamModel: route.UpstreamModel, ProxyProfileID: profile.ID, StatusCode: http.StatusBadGateway, ErrorCode: "upstream_error", Started: started, ClientIP: clientIP(r), UserAgent: r.UserAgent(), Usage: usage, EventType: "api_call", Message: err.Error()})
		return
	}
	defer response.Body.Close()
	usage, copyErr := copyCompatibleResponse(w, response, protocol)
	if copyErr != nil {
		log.Printf("copy upstream response request_id=%s err=%v", requestID, copyErr)
	}
	latencyMS := time.Since(started).Milliseconds()
	usage.RequestID = requestID
	usage.UserID = subject.UserID
	usage.APIKeyID = subject.APIKeyID
	usage.ProviderID = provider.ID
	usage.ModelAlias = model
	usage.UpstreamModel = route.UpstreamModel
	usage.Protocol = protocol
	usage.Endpoint = endpoint
	usage.Stream = stream
	usage.StatusCode = response.StatusCode
	usage.LatencyMS = latencyMS
	usage.CreatedAt = time.Now().UTC()
	if usage.Currency == "" {
		usage.Currency = "USD"
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
	handler.audit(context.WithoutCancel(r.Context()), auditInput{RequestID: requestID, Subject: subject, Protocol: protocol, Endpoint: endpoint, ModelAlias: model, ProviderID: provider.ID, UpstreamModel: route.UpstreamModel, ProxyProfileID: profile.ID, StatusCode: response.StatusCode, Started: started, ClientIP: clientIP(r), UserAgent: r.UserAgent(), Stream: stream, Usage: usage, EventType: "api_call"})
}

func (handler *Handler) authenticate(w http.ResponseWriter, r *http.Request, protocol, model string) (domain.Subject, bool) {
	key, err := auth.ExtractAPIKey(r)
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, "missing_api_key", "API key is required")
		return domain.Subject{}, false
	}
	apiKey, err := handler.store.FindAPIKeyByHash(r.Context(), auth.HashAPIKey(key))
	if err != nil || !auth.APIKeyActive(apiKey, time.Now().UTC()) {
		writeJSONError(w, http.StatusUnauthorized, "invalid_api_key", "API key is invalid or disabled")
		return domain.Subject{}, false
	}
	subject := auth.SubjectFromAPIKey(apiKey)
	if !auth.Allows(subject, protocol, model) {
		writeJSONError(w, http.StatusForbidden, "not_allowed", "API key does not allow this protocol or model")
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
	upstreamURL, err := joinUpstreamURL(provider.BaseURL, endpoint)
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
	if protocol == domain.ProtocolAnthropic {
		request.Header.Set("x-api-key", provider.MasterKey)
	} else {
		request.Header.Set("Authorization", "Bearer "+provider.MasterKey)
	}
	return request, nil
}

func copyRequestHeaders(dst, src http.Header, protocol string) {
	allowed := map[string]bool{
		"accept":              true,
		"content-type":        true,
		"user-agent":          true,
		"openai-organization": true,
		"openai-project":      true,
		"anthropic-version":   true,
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
	estimated := len(body) / 4
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err == nil {
		estimated += intNumber(payload["max_tokens"])
		estimated += intNumber(payload["max_completion_tokens"])
	}
	if estimated < 1 {
		return 1
	}
	return estimated
}

func copyCompatibleResponse(w http.ResponseWriter, response *http.Response, protocol string) (domain.UsageRecord, error) {
	copyResponseHeaders(w.Header(), response.Header)
	w.WriteHeader(response.StatusCode)
	contentType, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if contentType == "text/event-stream" {
		return copySSE(w, response.Body, protocol)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return domain.UsageRecord{}, err
	}
	if _, err := w.Write(body); err != nil {
		return domain.UsageRecord{}, err
	}
	return parseUsageFromJSON(body, protocol), nil
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
			usage := domain.UsageRecord{Currency: "USD", InputTokens: intNumber(payload["input_tokens"])}
			usage.TotalTokens = usage.InputTokens
			return usage
		}
	}
	if usagePayload == nil {
		return domain.UsageRecord{}
	}
	usage := domain.UsageRecord{Currency: "USD"}
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

type auditInput struct {
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
	ClientIP       string
	UserAgent      string
	Stream         bool
	Usage          domain.UsageRecord
	EventType      string
	Message        string
}

func (handler *Handler) audit(ctx context.Context, input auditInput) {
	if input.EventType == "" {
		input.EventType = "api_call"
	}
	event := domain.AuditEvent{
		ID:             newID("audit"),
		RequestID:      input.RequestID,
		UserID:         input.Subject.UserID,
		APIKeyID:       input.Subject.APIKeyID,
		ClientIP:       input.ClientIP,
		UserAgent:      input.UserAgent,
		Protocol:       input.Protocol,
		Endpoint:       input.Endpoint,
		ModelAlias:     input.ModelAlias,
		ProviderID:     input.ProviderID,
		UpstreamModel:  input.UpstreamModel,
		ProxyProfileID: input.ProxyProfileID,
		StatusCode:     input.StatusCode,
		ErrorCode:      input.ErrorCode,
		LatencyMS:      time.Since(input.Started).Milliseconds(),
		InputTokens:    input.Usage.InputTokens,
		OutputTokens:   input.Usage.OutputTokens,
		CostMicroCents: input.Usage.CostMicroCents,
		Stream:         input.Stream,
		EventType:      input.EventType,
		Message:        input.Message,
		CreatedAt:      time.Now().UTC(),
	}
	if err := handler.store.InsertAudit(ctx, event); err != nil {
		log.Printf("insert audit request_id=%s err=%v", input.RequestID, err)
	}
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
	writeJSON(w, status, map[string]any{"error": map[string]any{"code": code, "message": message}})
}
