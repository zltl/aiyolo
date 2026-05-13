package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/zltl/aiyolo/internal/domain"
)

var ErrMissingAPIKey = errors.New("missing API key")

func GenerateAPIKey(kind string) (string, error) {
	if kind == "" {
		kind = "live"
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	body := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw))
	return "aiyolo_" + kind + "_" + body, nil
}

func HashAPIKey(key string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(key)))
	return hex.EncodeToString(sum[:])
}

func Prefix(key string) string {
	key = strings.TrimSpace(key)
	if len(key) <= 16 {
		return key
	}
	return key[:16]
}

func ExtractAPIKey(r *http.Request) (string, error) {
	authorization := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(authorization), "bearer ") {
		return strings.TrimSpace(authorization[7:]), nil
	}
	if key := strings.TrimSpace(r.Header.Get("x-api-key")); key != "" {
		return key, nil
	}
	return "", ErrMissingAPIKey
}

func Allows(subject domain.Subject, protocol, model string) bool {
	if len(subject.AllowedProtocols) > 0 && !contains(subject.AllowedProtocols, protocol) {
		return false
	}
	if len(subject.AllowedModels) > 0 && model != "" && !contains(subject.AllowedModels, model) {
		return false
	}
	return true
}

func APIKeyActive(key domain.APIKey, now time.Time) bool {
	if key.Status != "" && key.Status != domain.StatusActive && key.Status != domain.StatusEnabled {
		return false
	}
	if key.ExpiresAt != nil && now.After(*key.ExpiresAt) {
		return false
	}
	return true
}

func SubjectFromAPIKey(key domain.APIKey) domain.Subject {
	return domain.Subject{
		APIKeyID:           key.ID,
		UserID:             key.UserID,
		OrganizationID:     key.OrganizationID,
		ProjectID:          key.ProjectID,
		AllowedProtocols:   key.AllowedProtocols,
		AllowedModels:      key.AllowedModels,
		RPMLimit:           key.RPMLimit,
		TPMLimit:           key.TPMLimit,
		ConcurrentLimit:    key.ConcurrentLimit,
		DailyBudgetCents:   key.DailyBudgetCents,
		MonthlyBudgetCents: key.MonthlyBudgetCents,
	}
}

func Sign(value, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))
}

func Verify(value, signature, secret string) bool {
	expected := Sign(value, secret)
	return hmac.Equal([]byte(expected), []byte(signature))
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
