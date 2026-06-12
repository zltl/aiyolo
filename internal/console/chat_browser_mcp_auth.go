package console

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/zltl/aiyolo/internal/auth"
)

const (
	consoleBrowserMCPTokenTTL    = 7 * 24 * time.Hour
	consoleBrowserMCPAuthHeader  = "Authorization"
	consoleBrowserMCPTokenPrefix   = "aiyolo_browser_mcp"
)

func consoleBrowserMCPURL(consoleBaseURL, chatSessionID string) string {
	consoleBaseURL = strings.TrimRight(strings.TrimSpace(consoleBaseURL), "/")
	chatSessionID = strings.TrimSpace(chatSessionID)
	if consoleBaseURL == "" || chatSessionID == "" {
		return ""
	}
	return consoleBaseURL + consoleChatBrowserMCPPath + "?session=" + chatSessionID
}

func (handler *Handler) issueBrowserMCPToken(userID, chatSessionID string) string {
	userID = strings.TrimSpace(userID)
	chatSessionID = strings.TrimSpace(chatSessionID)
	if userID == "" || chatSessionID == "" || strings.TrimSpace(handler.cfg.SecretKey) == "" {
		return ""
	}
	expires := time.Now().Add(consoleBrowserMCPTokenTTL).UTC().Unix()
	payload := strings.Join([]string{consoleBrowserMCPTokenPrefix, userID, chatSessionID, strconv.FormatInt(expires, 10)}, ":")
	return payload + ":" + auth.Sign(payload, handler.cfg.SecretKey)
}

func verifyBrowserMCPToken(secret, token, expectedSessionID string) (string, error) {
	token = strings.TrimSpace(token)
	expectedSessionID = strings.TrimSpace(expectedSessionID)
	if token == "" || expectedSessionID == "" {
		return "", errors.New("missing browser mcp token")
	}
	parts := strings.Split(token, ":")
	if len(parts) < 5 || parts[0] != consoleBrowserMCPTokenPrefix {
		return "", errors.New("invalid browser mcp token")
	}
	signature := parts[len(parts)-1]
	payload := strings.Join(parts[:len(parts)-1], ":")
	if !auth.Verify(payload, signature, secret) {
		return "", errors.New("invalid browser mcp token signature")
	}
	expires, err := strconv.ParseInt(parts[len(parts)-2], 10, 64)
	if err != nil || time.Now().Unix() >= expires {
		return "", errors.New("browser mcp token expired")
	}
	userID := strings.TrimSpace(parts[1])
	sessionID := strings.TrimSpace(parts[2])
	if userID == "" || sessionID == "" {
		return "", errors.New("invalid browser mcp token subject")
	}
	if sessionID != expectedSessionID {
		return "", errors.New("browser mcp token session mismatch")
	}
	return userID, nil
}

func extractBrowserMCPBearerToken(r *http.Request) string {
	if r == nil {
		return ""
	}
	authorization := strings.TrimSpace(r.Header.Get(consoleBrowserMCPAuthHeader))
	if strings.HasPrefix(strings.ToLower(authorization), "bearer ") {
		return strings.TrimSpace(authorization[7:])
	}
	return ""
}

func (handler *Handler) resolveBrowserMCPAccess(r *http.Request, chatSessionID string) (string, error) {
	chatSessionID = strings.TrimSpace(chatSessionID)
	if chatSessionID == "" {
		return "", errors.New("missing chat session")
	}
	if subject := currentConsoleSessionSubject(r, handler.cfg.SecretKey); subject != "" {
		return subject, nil
	}
	userID, err := verifyBrowserMCPToken(handler.cfg.SecretKey, extractBrowserMCPBearerToken(r), chatSessionID)
	if err != nil {
		return "", err
	}
	return userID, nil
}
