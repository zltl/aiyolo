package storage

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/zltl/aiyolo/internal/domain"
)

func actualUsageTokens(usage domain.UsageRecord) int {
	if usage.TotalTokens > 0 {
		return usage.TotalTokens
	}
	return usage.InputTokens + usage.OutputTokens + usage.CacheCreationTokens + usage.CacheReadTokens
}

func quotaSpentSince(ctx context.Context, tx pgx.Tx, apiKeyID string, since time.Time) (int64, error) {
	var spent int64
	err := tx.QueryRow(ctx, `
SELECT
  coalesce((SELECT sum(cost_micro_cents) FROM usage_ledger WHERE api_key_id = $1 AND created_at >= $2), 0) +
  coalesce((SELECT sum(greatest(estimated_cost_micro_cents, actual_cost_micro_cents)) FROM quota_reservations WHERE api_key_id = $1 AND status = 'reserved' AND created_at >= $2), 0)`, apiKeyID, since).Scan(&spent)
	return spent, err
}

func startOfDay(t time.Time) time.Time {
	year, month, day := t.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, t.Location())
}

func startOfMonth(t time.Time) time.Time {
	year, month, _ := t.Date()
	return time.Date(year, month, 1, 0, 0, 0, 0, t.Location())
}
