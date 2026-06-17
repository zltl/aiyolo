package console

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/sashabaranov/go-openai"
	"github.com/zltl/aiyolo/internal/artifacts"
	"github.com/zltl/aiyolo/internal/domain"
	"github.com/zltl/aiyolo/internal/storage"
)

const (
	consoleChatSessionLimit             = 24
	consoleChatSessionStatusReady       = "ready"
	consoleChatSessionStatusStreaming   = "streaming"
	consoleChatSessionStatusCompleted   = "completed"
	consoleChatSessionStatusInterrupted = "interrupted"
	consoleChatSessionStatusFailed      = "failed"
)

type consoleChatSessionView struct {
	ID               string                      `json:"id"`
	Title            string                      `json:"title"`
	CustomTitle      bool                        `json:"customTitle"`
	PublicName       string                      `json:"publicName"`
	SystemPrompt     string                      `json:"systemPrompt"`
	LastResponseID   string                      `json:"-"`
	Draft            string                      `json:"draft,omitempty"`
	DraftAttachments []consoleChatAttachmentView `json:"draftAttachments,omitempty"`
	Messages         []consoleChatMessageView    `json:"messages"`
	Status           string                      `json:"status,omitempty"`
	LastError        string                      `json:"lastError,omitempty"`
	CreatedAt        time.Time                   `json:"createdAt"`
	UpdatedAt        time.Time                   `json:"updatedAt"`
}

type consoleChatSessionStoreView struct {
	Version         int                      `json:"version"`
	ActiveSessionID string                   `json:"activeSessionId"`
	Sessions        []consoleChatSessionView `json:"sessions"`
}

func cloneConsoleChatMessages(messages []consoleChatMessageView) []consoleChatMessageView {
	if len(messages) == 0 {
		return nil
	}
	cloned := make([]consoleChatMessageView, 0, len(messages))
	for _, message := range messages {
		message.Attachments = cloneConsoleChatAttachments(message.Attachments)
		cloned = append(cloned, message)
	}
	return cloned
}

func normalizeConsoleChatSessionMessages(locale string, messages []consoleChatMessageView, cfg artifacts.Config) []consoleChatMessageView {
	if len(messages) == 0 {
		return nil
	}
	normalized := make([]consoleChatMessageView, 0, len(messages))
	for _, message := range messages {
		cleaned, ok := normalizeConsoleChatMessage(locale, message, cfg)
		if !ok {
			continue
		}
		normalized = append(normalized, cleaned)
	}
	return normalized
}

func decodeConsoleChatSessionMessages(locale string, raw string, cfg artifacts.Config) []consoleChatMessageView {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var messages []consoleChatMessageView
	if err := json.Unmarshal([]byte(raw), &messages); err != nil {
		return nil
	}
	return normalizeConsoleChatSessionMessages(locale, messages, cfg)
}

func normalizeConsoleChatSessionAttachments(cfg artifacts.Config, attachments []consoleChatAttachmentView) []consoleChatAttachmentView {
	if len(attachments) == 0 {
		return nil
	}
	normalized := make([]consoleChatAttachmentView, 0, len(attachments))
	for _, attachment := range attachments {
		cleaned, ok := normalizeConsoleChatAttachment(cfg, attachment)
		if !ok {
			continue
		}
		normalized = append(normalized, cleaned)
	}
	return normalized
}

func decodeConsoleChatSessionAttachments(raw string, cfg artifacts.Config) []consoleChatAttachmentView {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var attachments []consoleChatAttachmentView
	if err := json.Unmarshal([]byte(raw), &attachments); err != nil {
		return nil
	}
	return normalizeConsoleChatSessionAttachments(cfg, attachments)
}

func normalizeConsoleChatSessionView(locale string, session consoleChatSessionView, cfg artifacts.Config) consoleChatSessionView {
	session.ID = strings.TrimSpace(session.ID)
	session.Title = strings.TrimSpace(session.Title)
	session.PublicName = strings.TrimSpace(session.PublicName)
	session.SystemPrompt = strings.TrimSpace(session.SystemPrompt)
	session.Draft = strings.TrimSpace(session.Draft)
	session.LastError = strings.TrimSpace(session.LastError)
	session.DraftAttachments = normalizeConsoleChatSessionAttachments(cfg, session.DraftAttachments)
	session.Messages = normalizeConsoleChatSessionMessages(locale, session.Messages, cfg)
	return session
}

func consoleChatSessionViewFromDomain(locale string, session domain.ConsoleChatSession, cfg artifacts.Config) consoleChatSessionView {
	return consoleChatSessionView{
		ID:               session.ID,
		Title:            session.Title,
		CustomTitle:      session.CustomTitle,
		PublicName:       session.PublicName,
		SystemPrompt:     session.SystemPrompt,
		LastResponseID:   session.LastResponseID,
		Draft:            session.Draft,
		DraftAttachments: decodeConsoleChatSessionAttachments(session.DraftAttachmentsJSON, cfg),
		Messages:         decodeConsoleChatSessionMessages(locale, session.MessagesJSON, cfg),
		Status:           session.Status,
		LastError:        session.LastError,
		CreatedAt:        session.CreatedAt,
		UpdatedAt:        session.UpdatedAt,
	}
}

func (handler *Handler) loadConsoleChatSessionStore(ctx context.Context, r *http.Request, activeSessionID string) (consoleChatSessionStoreView, *consoleChatSessionView, error) {
	locale := resolveConsoleLocale(r)
	userID := currentConsoleSessionSubject(r, handler.cfg.SecretKey)
	records, err := handler.store.ListConsoleChatSessions(ctx, userID, consoleChatSessionLimit)
	if err != nil {
		return consoleChatSessionStoreView{}, nil, err
	}
	store := consoleChatSessionStoreView{Version: 1, Sessions: make([]consoleChatSessionView, 0, len(records))}
	activeSessionID = strings.TrimSpace(activeSessionID)
	var active *consoleChatSessionView
	for _, record := range records {
		view := consoleChatSessionViewFromDomain(locale, record, handler.cfg.ChatAttachments)
		store.Sessions = append(store.Sessions, view)
		if active == nil && activeSessionID != "" && view.ID == activeSessionID {
			copy := view
			active = &copy
		}
	}
	if active == nil && len(store.Sessions) > 0 {
		if activeSessionID == "" {
			activeSessionID = store.Sessions[0].ID
		}
		for index := range store.Sessions {
			if store.Sessions[index].ID == activeSessionID {
				copy := store.Sessions[index]
				active = &copy
				break
			}
		}
		if active == nil {
			copy := store.Sessions[0]
			active = &copy
		}
	}
	if active != nil {
		store.ActiveSessionID = active.ID
	}
	return store, active, nil
}

func consoleChatSessionDefaultTitle(locale string) string {
	return consoleText(locale, "新会话", "New chat")
}

func truncateConsoleChatSessionTitle(value string, limit int) string {
	compact := strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if compact == "" || limit <= 0 {
		return compact
	}
	if len(compact) <= limit {
		return compact
	}
	return compact[:limit-1] + "…"
}

func consoleChatSessionSeedTitle(messages []consoleChatMessageView) string {
	for _, message := range messages {
		if normalizeConsoleChatRole(message.Role) != "user" {
			continue
		}
		if trimmed := truncateConsoleChatSessionTitle(message.Content, 56); trimmed != "" {
			return trimmed
		}
		if len(message.Attachments) > 0 {
			if trimmed := truncateConsoleChatSessionTitle(message.Attachments[0].Name, 56); trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

func consoleChatSessionTitle(locale string, title string, customTitle bool, publicName string, messages []consoleChatMessageView, draft string, draftAttachments []consoleChatAttachmentView) string {
	if customTitle {
		if trimmed := strings.TrimSpace(title); trimmed != "" {
			return trimmed
		}
	}
	if seeded := consoleChatSessionSeedTitle(messages); seeded != "" {
		return seeded
	}
	return consoleChatSessionDefaultTitle(locale)
}

func consoleChatSessionCanGenerateTitle(messages []consoleChatMessageView) bool {
	seenUser := false
	seenAssistant := false
	for _, message := range messages {
		switch normalizeConsoleChatRole(message.Role) {
		case openai.ChatMessageRoleUser:
			if strings.TrimSpace(message.Content) != "" || len(message.Attachments) > 0 {
				seenUser = true
			}
		case openai.ChatMessageRoleAssistant:
			if strings.TrimSpace(message.Content) != "" || strings.TrimSpace(message.Reasoning) != "" {
				seenAssistant = true
			}
		}
		if seenUser && seenAssistant {
			return true
		}
	}
	return false
}

func consoleChatSessionNeedsGeneratedTitle(locale string, currentTitle string, customTitle bool, publicName string, messages []consoleChatMessageView, draft string, draftAttachments []consoleChatAttachmentView) bool {
	if customTitle || !consoleChatSessionCanGenerateTitle(messages) {
		return false
	}
	currentTitle = strings.TrimSpace(currentTitle)
	if currentTitle == "" {
		return true
	}
	heuristicTitle := consoleChatSessionTitle(locale, "", false, publicName, messages, draft, draftAttachments)
	if currentTitle == heuristicTitle {
		return true
	}
	if currentTitle == strings.TrimSpace(publicName) {
		return true
	}
	return currentTitle == consoleChatSessionDefaultTitle(locale)
}

func consoleChatResolvedSessionTitle(locale string, currentTitle string, customTitle bool, publicName string, messages []consoleChatMessageView, draft string, draftAttachments []consoleChatAttachmentView, generatedTitle string) string {
	currentTitle = strings.TrimSpace(currentTitle)
	if customTitle {
		if currentTitle != "" {
			return currentTitle
		}
		return consoleChatSessionTitle(locale, currentTitle, true, publicName, messages, draft, draftAttachments)
	}
	if !consoleChatSessionCanGenerateTitle(messages) {
		return consoleChatSessionTitle(locale, currentTitle, false, publicName, messages, draft, draftAttachments)
	}
	if generatedTitle = normalizeConsoleChatGeneratedTitle(generatedTitle); generatedTitle != "" {
		return generatedTitle
	}
	if currentTitle != "" && !consoleChatSessionNeedsGeneratedTitle(locale, currentTitle, false, publicName, messages, draft, draftAttachments) {
		return currentTitle
	}
	return consoleChatSessionTitle(locale, currentTitle, false, publicName, messages, draft, draftAttachments)
}

func consoleChatSessionTitleSystemPrompt(locale string) string {
	return consoleText(locale,
		"你负责给聊天会话生成标题。只输出一个简短标题，不要引号、序号、前缀、句号或额外解释。尽量使用对话本身的语言，控制在 12 个词以内。",
		"You write concise titles for chat sessions. Return exactly one short plain-text title with no quotes, numbering, prefix, period, or extra explanation. Use the conversation's original language when possible and keep it under 12 words.")
}

func consoleChatSessionTitlePromptLine(locale string, message consoleChatMessageView) string {
	text := strings.TrimSpace(message.Content)
	if text == "" {
		text = strings.TrimSpace(message.Reasoning)
	}
	if text == "" && len(message.Attachments) > 0 {
		names := make([]string, 0, len(message.Attachments))
		for _, attachment := range message.Attachments {
			if name := strings.TrimSpace(attachment.Name); name != "" {
				names = append(names, name)
			}
		}
		if len(names) > 0 {
			text = consoleText(locale, "附件：", "Attachments: ") + strings.Join(names, ", ")
		}
	}
	if text == "" {
		return ""
	}
	return consoleChatRoleLabel(locale, message.Role) + ": " + truncateConsoleChatSessionTitle(text, 240)
}

func consoleChatSessionTitlePrompt(locale string, messages []consoleChatMessageView) string {
	lines := []string{consoleText(locale,
		"请根据下面这段对话生成一个准确、简洁的标题：",
		"Generate a concise and accurate title for this conversation:")}
	count := 0
	for _, message := range messages {
		role := normalizeConsoleChatRole(message.Role)
		if role == "" || role == openai.ChatMessageRoleSystem {
			continue
		}
		line := consoleChatSessionTitlePromptLine(locale, message)
		if line == "" {
			continue
		}
		lines = append(lines, line)
		count++
		if count >= 8 {
			break
		}
	}
	return strings.Join(lines, "\n")
}

func normalizeConsoleChatGeneratedTitle(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if line, _, ok := strings.Cut(raw, "\n"); ok {
		raw = line
	}
	raw = strings.TrimSpace(raw)
	raw = strings.Trim(raw, "\"'`“”‘’")
	raw = strings.TrimLeft(raw, "-*#0123456789.) \t")
	for {
		switch {
		case strings.HasPrefix(strings.ToLower(raw), "title:"):
			raw = strings.TrimSpace(raw[len("title:"):])
		case strings.HasPrefix(raw, "标题："):
			raw = strings.TrimSpace(strings.TrimPrefix(raw, "标题："))
		case strings.HasPrefix(raw, "标题:"):
			raw = strings.TrimSpace(strings.TrimPrefix(raw, "标题:"))
		default:
			raw = strings.Trim(raw, " \t\r\n-:;,.!?，。！？")
			return truncateConsoleChatSessionTitle(raw, 56)
		}
	}
}

func (handler *Handler) generateConsoleChatSessionTitle(ctx context.Context, r *http.Request, currentTitle string, customTitle bool, publicName string, draft string, draftAttachments []consoleChatAttachmentView, messages []consoleChatMessageView) string {
	locale := resolveConsoleLocale(r)
	if !consoleChatSessionNeedsGeneratedTitle(locale, currentTitle, customTitle, publicName, messages, draft, draftAttachments) {
		return ""
	}
	target, errorMessage := handler.resolveConsoleChatTarget(ctx, r, publicName)
	if errorMessage != "" {
		log.Printf("console chat title generation skipped public_name=%s reason=%s", strings.TrimSpace(publicName), errorMessage)
		return ""
	}
	titleCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	execution, err := runConsoleChatTurn(titleCtx, target.Provider, target.Route, target.Profile, consoleChatSessionTitleSystemPrompt(locale), "", nil, consoleChatSessionTitlePrompt(locale, messages), nil)
	if err != nil {
		log.Printf("console chat title generation failed public_name=%s err=%v", strings.TrimSpace(publicName), err)
		return ""
	}
	title := normalizeConsoleChatGeneratedTitle(consoleChatContinuationContent(execution.Result))
	if title == "" {
		log.Printf("console chat title generation returned empty title public_name=%s", strings.TrimSpace(publicName))
	}
	return title
}

func consoleChatSessionStatusForError(result consoleChatResultView) string {
	if strings.TrimSpace(result.Output) != "" || strings.TrimSpace(result.Reasoning) != "" {
		return consoleChatSessionStatusInterrupted
	}
	return consoleChatSessionStatusFailed
}

func consoleChatAppendResultMessage(locale string, messages []consoleChatMessageView, result consoleChatResultView) []consoleChatMessageView {
	apiMessage := consoleChatStreamMessage(locale, result)
	if apiMessage == nil {
		return cloneConsoleChatMessages(messages)
	}
	next := cloneConsoleChatMessages(messages)
	return append(next, buildConsoleChatMessageWithReasoning(locale, apiMessage.Role, apiMessage.Content, apiMessage.Reasoning))
}

func consoleChatAppendAssistantProgress(locale string, messages []consoleChatMessageView, content string, reasoning string, operations []consoleChatStreamOperation) []consoleChatMessageView {
	next := cloneConsoleChatMessages(messages)
	if strings.TrimSpace(content) == "" && strings.TrimSpace(reasoning) == "" && len(operations) == 0 {
		return next
	}
	return append(next, buildConsoleChatMessageWithProgress(locale, "assistant", content, reasoning, operations))
}

func consoleChatSessionMessagesPayload(messages []consoleChatMessageView) (string, error) {
	payload, err := json.Marshal(messages)
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

func consoleChatSessionCanonicalMessagesJSON(locale string, raw string, cfg artifacts.Config) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "[]"
	}
	payload, err := consoleChatSessionMessagesPayload(decodeConsoleChatSessionMessages(locale, trimmed, cfg))
	if err != nil {
		return trimmed
	}
	return payload
}

func consoleChatSessionHasMessageActivity(locale string, existingJSON string, nextJSON string, cfg artifacts.Config) bool {
	return consoleChatSessionCanonicalMessagesJSON(locale, existingJSON, cfg) != consoleChatSessionCanonicalMessagesJSON(locale, nextJSON, cfg)
}

func consoleChatSessionMessagePayloadSize(messages []consoleChatMessageView) int {
	payload, err := consoleChatSessionMessagesPayload(messages)
	if err != nil {
		return 0
	}
	return len(payload)
}

func consoleChatPreferRicherSessionMessages(locale string, existingJSON string, incoming []consoleChatMessageView, cfg artifacts.Config) []consoleChatMessageView {
	existing := decodeConsoleChatSessionMessages(locale, existingJSON, cfg)
	incoming = normalizeConsoleChatSessionMessages(locale, incoming, cfg)
	if len(incoming) == 0 {
		return existing
	}
	if len(existing) == 0 {
		return incoming
	}
	if len(existing) > len(incoming) {
		return existing
	}
	if len(incoming) > len(existing) {
		return incoming
	}
	if consoleChatSessionMessagePayloadSize(incoming) > consoleChatSessionMessagePayloadSize(existing) {
		return incoming
	}
	return existing
}

func (handler *Handler) persistConsoleChatSession(ctx context.Context, r *http.Request, sessionID string, publicName string, systemPrompt string, draft string, draftAttachments []consoleChatAttachmentView, messages []consoleChatMessageView, status string, requestID string, responseID string, lastError string) (domain.ConsoleChatSession, error) {
	locale := resolveConsoleLocale(r)
	userID := currentConsoleSessionSubject(r, handler.cfg.SecretKey)
	return handler.persistConsoleChatSessionForUser(ctx, locale, userID, sessionID, publicName, systemPrompt, draft, draftAttachments, messages, status, requestID, responseID, lastError)
}

func (handler *Handler) persistConsoleChatSessionForUser(ctx context.Context, locale string, userID string, sessionID string, publicName string, systemPrompt string, draft string, draftAttachments []consoleChatAttachmentView, messages []consoleChatMessageView, status string, requestID string, responseID string, lastError string) (domain.ConsoleChatSession, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		sessionID = newConsoleID("chat")
	}
	userID = strings.TrimSpace(userID)
	normalizedDraftAttachments := normalizeConsoleChatSessionAttachments(handler.cfg.ChatAttachments, cloneConsoleChatAttachments(draftAttachments))
	existing, err := handler.store.GetConsoleChatSession(ctx, userID, sessionID)
	if err != nil && err != storage.ErrNotFound {
		return domain.ConsoleChatSession{}, err
	}
	normalizedMessages := consoleChatPreferRicherSessionMessages(locale, existing.MessagesJSON, messages, handler.cfg.ChatAttachments)
	messagesJSON, err := consoleChatSessionMessagesPayload(normalizedMessages)
	if err != nil {
		return domain.ConsoleChatSession{}, err
	}
	messageActivity := consoleChatSessionHasMessageActivity(locale, existing.MessagesJSON, messagesJSON, handler.cfg.ChatAttachments)
	now := time.Now().UTC()
	session := existing
	session.ID = sessionID
	session.UserID = userID
	session.PublicName = firstNonEmpty(strings.TrimSpace(publicName), session.PublicName)
	session.SystemPrompt = firstNonEmpty(strings.TrimSpace(systemPrompt), session.SystemPrompt)
	session.Draft = strings.TrimSpace(draft)
	session.Status = firstNonEmpty(strings.TrimSpace(status), session.Status, consoleChatSessionStatusReady)
	session.LastRequestID = firstNonEmpty(strings.TrimSpace(requestID), session.LastRequestID)
	session.LastResponseID = firstNonEmpty(strings.TrimSpace(responseID), session.LastResponseID)
	session.LastError = strings.TrimSpace(lastError)
	if session.CreatedAt.IsZero() {
		session.CreatedAt = now
	}
	if session.UpdatedAt.IsZero() {
		session.UpdatedAt = session.CreatedAt
	}
	if messageActivity {
		session.UpdatedAt = now
	}
	session.MessageCount = len(normalizedMessages)
	if messageActivity && session.MessageCount > 0 {
		lastMessageAt := now
		session.LastMessageAt = &lastMessageAt
	} else if session.MessageCount == 0 {
		session.LastMessageAt = nil
	}
	switch session.Status {
	case consoleChatSessionStatusCompleted, consoleChatSessionStatusInterrupted, consoleChatSessionStatusFailed:
		completedAt := now
		session.CompletedAt = &completedAt
	default:
		session.CompletedAt = nil
	}
	session.Title = consoleChatSessionTitle(locale, session.Title, session.CustomTitle, session.PublicName, normalizedMessages, session.Draft, normalizedDraftAttachments)
	session.DraftAttachmentsJSON = consoleChatJSON(normalizedDraftAttachments)
	session.MessagesJSON = messagesJSON
	if err := handler.store.UpsertConsoleChatSession(ctx, session); err != nil {
		return domain.ConsoleChatSession{}, err
	}
	return session, nil
}

func (handler *Handler) syncConsoleChatPageSession(ctx context.Context, r *http.Request, state *consoleChatPageState, messages []consoleChatMessageView, status string, requestID string, responseID string, lastError string) {
	if state == nil {
		return
	}
	session, err := handler.persistConsoleChatSession(ctx, r, state.Form.ClientSessionID, state.Form.PublicName, state.Form.SystemPrompt, state.Form.Draft, state.Form.Attachments, messages, status, requestID, responseID, lastError)
	if err != nil {
		log.Printf("console chat session persist failed request_id=%s session_id=%s err=%v", requestID, strings.TrimSpace(state.Form.ClientSessionID), err)
		return
	}
	state.LastResponseID = strings.TrimSpace(session.LastResponseID)
	state.Form.ClientSessionID = session.ID
	sessionStore, _, err := handler.loadConsoleChatSessionStore(ctx, r, session.ID)
	if err != nil {
		log.Printf("console chat session store reload failed request_id=%s session_id=%s err=%v", requestID, session.ID, err)
		return
	}
	state.SessionStore = sessionStore
}

func (handler *Handler) saveChatSession(w http.ResponseWriter, r *http.Request) {
	var payload consoleChatSessionView
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	locale := resolveConsoleLocale(r)
	payload = normalizeConsoleChatSessionView(locale, payload, handler.cfg.ChatAttachments)
	if payload.ID == "" {
		payload.ID = newConsoleID("chat")
	}
	userID := currentConsoleSessionSubject(r, handler.cfg.SecretKey)
	existing, err := handler.store.GetConsoleChatSession(r.Context(), userID, payload.ID)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	messages := consoleChatPreferRicherSessionMessages(locale, existing.MessagesJSON, payload.Messages, handler.cfg.ChatAttachments)
	messagesJSON, err := consoleChatSessionMessagesPayload(messages)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	messageActivity := consoleChatSessionHasMessageActivity(locale, existing.MessagesJSON, messagesJSON, handler.cfg.ChatAttachments)
	now := time.Now().UTC()
	session := existing
	session.ID = payload.ID
	session.UserID = userID
	session.PublicName = firstNonEmpty(payload.PublicName, session.PublicName)
	session.SystemPrompt = firstNonEmpty(payload.SystemPrompt, session.SystemPrompt)
	session.Draft = payload.Draft
	session.DraftAttachmentsJSON = consoleChatJSON(payload.DraftAttachments)
	session.CustomTitle = payload.CustomTitle
	currentTitle := strings.TrimSpace(session.Title)
	if currentTitle == "" {
		currentTitle = strings.TrimSpace(payload.Title)
	}
	generatedTitle := handler.generateConsoleChatSessionTitle(r.Context(), r, currentTitle, payload.CustomTitle, session.PublicName, payload.Draft, payload.DraftAttachments, messages)
	session.Title = consoleChatResolvedSessionTitle(locale, currentTitle, payload.CustomTitle, session.PublicName, messages, payload.Draft, payload.DraftAttachments, generatedTitle)
	session.Status = firstNonEmpty(strings.TrimSpace(payload.Status), session.Status, consoleChatSessionStatusReady)
	if handler.hasActiveConsoleCloudAgentRun(userID, payload.ID) {
		switch strings.TrimSpace(session.Status) {
		case consoleChatSessionStatusInterrupted, consoleChatSessionStatusFailed, consoleChatSessionStatusReady, "":
			session.Status = consoleChatSessionStatusStreaming
		}
	}
	if session.CreatedAt.IsZero() {
		session.CreatedAt = now
	}
	if session.UpdatedAt.IsZero() {
		session.UpdatedAt = session.CreatedAt
	}
	if messageActivity {
		session.UpdatedAt = now
	}
	session.LastError = firstNonEmpty(payload.LastError, session.LastError)
	if session.Status == consoleChatSessionStatusStreaming && handler.hasActiveConsoleCloudAgentRun(userID, payload.ID) {
		session.LastError = ""
	}
	session.MessageCount = len(messages)
	if messageActivity && session.MessageCount > 0 {
		lastMessageAt := now
		session.LastMessageAt = &lastMessageAt
	} else if session.MessageCount == 0 {
		session.LastMessageAt = nil
	}
	session.MessagesJSON = messagesJSON
	if err := handler.store.UpsertConsoleChatSession(r.Context(), session); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(consoleChatSessionViewFromDomain(locale, session, handler.cfg.ChatAttachments))
}

func (handler *Handler) deleteChatSession(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(chi.URLParam(r, "sessionID"))
	if sessionID == "" {
		http.Error(w, "missing session id", http.StatusBadRequest)
		return
	}
	userID := currentConsoleSessionSubject(r, handler.cfg.SecretKey)
	if err := handler.store.DeleteConsoleChatSession(r.Context(), userID, sessionID); err != nil && !errors.Is(err, storage.ErrNotFound) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
