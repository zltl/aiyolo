package domain

import (
	"strings"

	"github.com/google/uuid"
)

func CloudAgentClaudeSessionID(userID string, chatSessionID string) string {
	token := strings.TrimSpace(userID) + ":" + strings.TrimSpace(chatSessionID)
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("aiyolo-console-cloud-agent:"+token)).String()
}