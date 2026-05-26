package console

import (
	"context"
	"encoding/base64"
	"strings"

	"github.com/zltl/aiyolo/internal/domain"
)

func (handler *Handler) runConsoleChatTurn(ctx context.Context, provider domain.Provider, route domain.ModelRoute, profile domain.ProxyProfile, systemPrompt string, reasoningEffort string, history []consoleChatMessageView, userInput string, attachments []consoleChatAttachmentView) (consoleChatExecution, error) {
	protocol := handler.consoleChatExecutionProtocol(route, provider, history, attachments)
	if protocol == "" {
		return consoleChatExecution{StatusCode: 400, Usage: domain.UsageRecord{Currency: "USD", StatusCode: 400}}, &consoleUpstreamError{StatusCode: 400, Code: "unsupported_protocol", Message: "unsupported chat protocol"}
	}
	preparedHistory, preparedAttachments, err := handler.prepareConsoleChatAttachmentsForProvider(ctx, protocol, provider, history, attachments)
	if err != nil {
		return consoleChatExecution{StatusCode: 502, Usage: domain.UsageRecord{Currency: "USD", StatusCode: 502}}, err
	}
	return runConsoleChatTurnWithContinuation(ctx, protocol, provider, route, profile, systemPrompt, reasoningEffort, preparedHistory, userInput, preparedAttachments, false, nil, nil)
}

func (handler *Handler) runConsoleChatTurnWithContinuation(ctx context.Context, protocol string, provider domain.Provider, route domain.ModelRoute, profile domain.ProxyProfile, systemPrompt string, reasoningEffort string, history []consoleChatMessageView, userInput string, attachments []consoleChatAttachmentView, stream bool, onDelta func(string) error, onReasoning func(string) error) (consoleChatExecution, error) {
	protocol = handler.consoleChatExecutionProtocol(route, provider, history, attachments)
	if protocol == "" {
		return consoleChatExecution{StatusCode: 400, Usage: domain.UsageRecord{Currency: "USD", StatusCode: 400, Stream: stream}}, &consoleUpstreamError{StatusCode: 400, Code: "unsupported_protocol", Message: "unsupported chat protocol"}
	}
	preparedHistory, preparedAttachments, err := handler.prepareConsoleChatAttachmentsForProvider(ctx, protocol, provider, history, attachments)
	if err != nil {
		return consoleChatExecution{StatusCode: 502, Usage: domain.UsageRecord{Currency: "USD", StatusCode: 502, Stream: stream}}, err
	}
	return runConsoleChatTurnWithContinuation(ctx, protocol, provider, route, profile, systemPrompt, reasoningEffort, preparedHistory, userInput, preparedAttachments, stream, onDelta, onReasoning)
}

func (handler *Handler) consoleChatExecutionProtocol(route domain.ModelRoute, provider domain.Provider, history []consoleChatMessageView, attachments []consoleChatAttachmentView) string {
	protocol := consoleChatRouteProtocol(route, provider)
	if protocol == "" {
		return ""
	}
	if !domain.IsDeepSeekProvider(provider) {
		return protocol
	}
	if !consoleChatMessagesContainImageAttachments(history) && !consoleChatAttachmentsContainImages(attachments) {
		return protocol
	}
	if domain.SupportsProtocol(domain.ProviderSupportedProtocols(provider), domain.ProtocolAnthropic) {
		return domain.ProtocolAnthropic
	}
	return protocol
}

func (handler *Handler) prepareConsoleChatAttachmentsForProvider(ctx context.Context, protocol string, provider domain.Provider, history []consoleChatMessageView, attachments []consoleChatAttachmentView) ([]consoleChatMessageView, []consoleChatAttachmentView, error) {
	preparedHistory := cloneConsoleChatMessages(history)
	preparedAttachments := cloneConsoleChatAttachments(attachments)
	if !domain.IsDeepSeekProvider(provider) {
		return preparedHistory, preparedAttachments, nil
	}
	if protocol != domain.ProtocolOpenAI && protocol != domain.ProtocolAnthropic {
		return preparedHistory, preparedAttachments, nil
	}
	if !consoleChatMessagesContainImageAttachments(history) && !consoleChatAttachmentsContainImages(attachments) {
		return preparedHistory, preparedAttachments, nil
	}
	reader, err := handler.newChatAttachmentReader(handler.cfg.ChatAttachments)
	if err != nil {
		return nil, nil, err
	}
	for index := range preparedHistory {
		preparedHistory[index].Attachments, err = inlineConsoleChatImageAttachments(ctx, reader, preparedHistory[index].Attachments)
		if err != nil {
			return nil, nil, err
		}
	}
	preparedAttachments, err = inlineConsoleChatImageAttachments(ctx, reader, preparedAttachments)
	if err != nil {
		return nil, nil, err
	}
	return preparedHistory, preparedAttachments, nil
}

func consoleChatMessagesContainImageAttachments(messages []consoleChatMessageView) bool {
	for _, message := range messages {
		if consoleChatAttachmentsContainImages(message.Attachments) {
			return true
		}
	}
	return false
}

func consoleChatAttachmentsContainImages(attachments []consoleChatAttachmentView) bool {
	for _, attachment := range attachments {
		if consoleChatAttachmentIsImage(attachment.MediaType) {
			return true
		}
	}
	return false
}

func inlineConsoleChatImageAttachments(ctx context.Context, reader consoleChatAttachmentObjectReader, attachments []consoleChatAttachmentView) ([]consoleChatAttachmentView, error) {
	if len(attachments) == 0 {
		return nil, nil
	}
	prepared := cloneConsoleChatAttachments(attachments)
	for index := range prepared {
		if !consoleChatAttachmentIsImage(prepared[index].MediaType) {
			continue
		}
		payload, mediaType, err := reader.ReadObject(ctx, prepared[index].ObjectKey)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(mediaType) != "" {
			prepared[index].MediaType = strings.TrimSpace(mediaType)
		}
		prepared[index].URL = consoleChatAttachmentDataURL(prepared[index].MediaType, payload)
	}
	return prepared, nil
}

func consoleChatAttachmentDataURL(mediaType string, payload []byte) string {
	trimmedMediaType := strings.TrimSpace(mediaType)
	if trimmedMediaType == "" {
		trimmedMediaType = "application/octet-stream"
	}
	return "data:" + trimmedMediaType + ";base64," + base64.StdEncoding.EncodeToString(payload)
}
