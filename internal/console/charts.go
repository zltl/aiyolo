package console

import (
	"strconv"
	"strings"
	"time"

	"github.com/zltl/aiyolo/internal/domain"
)

func usageSparkline(records []domain.UsageRecord, metric string, buckets int) string {
	if buckets < 2 {
		buckets = 2
	}
	if len(records) == 0 {
		return flatSparklinePath(buckets)
	}

	oldest := records[0].CreatedAt
	newest := records[0].CreatedAt
	for _, record := range records[1:] {
		if record.CreatedAt.Before(oldest) {
			oldest = record.CreatedAt
		}
		if record.CreatedAt.After(newest) {
			newest = record.CreatedAt
		}
	}
	if !newest.After(oldest) {
		newest = oldest.Add(time.Second)
	}

	values := make([]int64, buckets)
	var uniqueValues []map[string]struct{}
	if metric == "users" || metric == "models" {
		uniqueValues = make([]map[string]struct{}, buckets)
	}
	span := newest.Sub(oldest)
	for _, record := range records {
		position := float64(record.CreatedAt.Sub(oldest)) / float64(span)
		index := int(position * float64(buckets-1))
		if index < 0 {
			index = 0
		}
		if index >= buckets {
			index = buckets - 1
		}
		switch metric {
		case "errors":
			if record.StatusCode >= 400 {
				values[index]++
			}
		case "users":
			if record.UserID == "" {
				continue
			}
			if uniqueValues[index] == nil {
				uniqueValues[index] = make(map[string]struct{})
			}
			uniqueValues[index][record.UserID] = struct{}{}
		case "models":
			if record.ModelAlias == "" {
				continue
			}
			if uniqueValues[index] == nil {
				uniqueValues[index] = make(map[string]struct{})
			}
			uniqueValues[index][record.ModelAlias] = struct{}{}
		case "spend":
			values[index] += record.CostMicroCents
		case "input":
			values[index] += int64(record.InputTokens)
		case "output":
			values[index] += int64(record.OutputTokens)
		default:
			values[index]++
		}
	}
	if uniqueValues != nil {
		for index, bucket := range uniqueValues {
			values[index] = int64(len(bucket))
		}
	}

	return sparklinePath(values)
}

func countShare(count int, total int64) string {
	if count <= 0 || total <= 0 {
		return "0%"
	}
	percent := int64(count) * 100 / total
	if percent > 100 {
		percent = 100
	}
	return strconv.FormatInt(percent, 10) + "%"
}

func flatSparklinePath(points int) string {
	values := make([]int64, points)
	return sparklinePath(values)
}

func sparklinePath(values []int64) string {
	if len(values) == 0 {
		return "M4 34 L116 34"
	}

	var maxValue int64
	for _, value := range values {
		if value > maxValue {
			maxValue = value
		}
	}

	const (
		xStart = 4.0
		xEnd   = 116.0
		yTop   = 6.0
		yBase  = 34.0
	)
	step := 0.0
	if len(values) > 1 {
		step = (xEnd - xStart) / float64(len(values)-1)
	}

	var builder strings.Builder
	for index, value := range values {
		x := xStart + float64(index)*step
		y := yBase
		if maxValue > 0 {
			y = yBase - (float64(value)/float64(maxValue))*(yBase-yTop)
		}
		if index == 0 {
			builder.WriteString("M")
		} else {
			builder.WriteString(" L")
		}
		builder.WriteString(strconv.FormatFloat(x, 'f', 1, 64))
		builder.WriteByte(' ')
		builder.WriteString(strconv.FormatFloat(y, 'f', 1, 64))
	}
	return builder.String()
}