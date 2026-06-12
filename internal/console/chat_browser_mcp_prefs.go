package console

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/zltl/aiyolo/internal/storage"
)

func consoleChatBrowserMCPEnabledFromState(state consoleChatShellState) bool {
	if state.BrowserMCPEnabled == nil {
		return true
	}
	return *state.BrowserMCPEnabled
}

func (handler *Handler) consoleChatBrowserMCPEnabled(ctx context.Context, userID, chatSessionID string) (bool, error) {
	userID = strings.TrimSpace(userID)
	chatSessionID = strings.TrimSpace(chatSessionID)
	if userID == "" || chatSessionID == "" {
		return true, nil
	}
	cloudSession, err := handler.store.GetCloudAgentSession(ctx, userID, consoleChatCloudAgentSessionID(chatSessionID))
	if errors.Is(err, storage.ErrNotFound) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	state := consoleChatShellStateFromSession(cloudSession, chatSessionID, cloudSession.WorkerID, "", cloudSession.WorkspacePath)
	return consoleChatBrowserMCPEnabledFromState(state), nil
}

func (handler *Handler) consoleCloudAgentBrowserMCPCodexOptions(ctx context.Context, userID, consoleBaseURL, chatSessionID string) (string, string) {
	enabled, err := handler.consoleChatBrowserMCPEnabled(ctx, userID, chatSessionID)
	if err != nil || !enabled {
		return "", ""
	}
	return consoleBrowserMCPURL(consoleBaseURL, chatSessionID), handler.issueBrowserMCPToken(userID, chatSessionID)
}

func (handler *Handler) consoleCloudAgentBrowserMCPStartOptions(ctx context.Context, userID, baseURL, chatSessionID string) (string, string) {
	return handler.consoleCloudAgentBrowserMCPCodexOptions(ctx, userID, baseURL, chatSessionID)
}

func (handler *Handler) setConsoleChatBrowserMCPEnabled(ctx context.Context, userID, chatSessionID string, enabled bool) error {
	userID = strings.TrimSpace(userID)
	chatSessionID = strings.TrimSpace(chatSessionID)
	if userID == "" || chatSessionID == "" {
		return errors.New("missing chat session")
	}
	cloudSession, err := handler.store.GetCloudAgentSession(ctx, userID, consoleChatCloudAgentSessionID(chatSessionID))
	if errors.Is(err, storage.ErrNotFound) {
		return storage.ErrNotFound
	}
	if err != nil {
		return err
	}
	state := consoleChatShellStateFromSession(cloudSession, chatSessionID, cloudSession.WorkerID, "", cloudSession.WorkspacePath)
	state.BrowserMCPEnabled = &enabled
	state.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	cloudSession.ShellStateJSON = consoleChatShellStatePayload(state)
	return handler.store.UpsertCloudAgentSession(ctx, cloudSession)
}
