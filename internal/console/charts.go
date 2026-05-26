package console

import (
	"strconv"
	"strings"
	"time"

	"github.com/zltl/aiyolo/internal/domain"
)

type sparklinePoint struct {
	x float64
	y float64
}

func usageSparkline(records []domain.UsageRecord, metric string, buckets int) string {
	values := usageMetricBuckets(records, metric, buckets)
	return sparklinePath(values)
}

func usageArea(records []domain.UsageRecord, metric string, buckets int) string {
	values := usageMetricBuckets(records, metric, buckets)
	return sparklineAreaPath(values)
}

func usageMetricBuckets(records []domain.UsageRecord, metric string, buckets int) []int64 {
	if buckets < 2 {
		buckets = 2
	}
	if len(records) == 0 {
		return make([]int64, buckets)
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

	return values
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

func sparklineAreaPath(values []int64) string {
	if len(values) == 0 {
		return "M4 34 L116 34 L116 34 L4 34 Z"
	}

	line := sparklinePath(values)
	return line + " L116 34.0 L4 34.0 Z"
}

func sparklinePath(values []int64) string {
	if len(values) == 0 {
		return "M4 34 L116 34"
	}

	points := sparklinePoints(values)
	if len(points) == 0 {
		return "M4 34 L116 34"
	}

	var builder strings.Builder
	builder.WriteString("M")
	builder.WriteString(pathFloat(points[0].x))
	builder.WriteByte(' ')
	builder.WriteString(pathFloat(points[0].y))

	for index := 1; index < len(points); index++ {
		previous := points[index-1]
		current := points[index]
		controlX := previous.x + (current.x-previous.x)/2

		builder.WriteString(" C")
		builder.WriteString(pathFloat(controlX))
		builder.WriteByte(' ')
		builder.WriteString(pathFloat(previous.y))
		builder.WriteByte(' ')
		builder.WriteString(pathFloat(controlX))
		builder.WriteByte(' ')
		builder.WriteString(pathFloat(current.y))
		builder.WriteByte(' ')
		builder.WriteString(pathFloat(current.x))
		builder.WriteByte(' ')
		builder.WriteString(pathFloat(current.y))
	}
	return builder.String()
}

func sparklinePoints(values []int64) []sparklinePoint {
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

	points := make([]sparklinePoint, 0, len(values))
	for index, value := range values {
		x := xStart + float64(index)*step
		y := yBase
		if maxValue > 0 {
			y = yBase - (float64(value)/float64(maxValue))*(yBase-yTop)
		}
		points = append(points, sparklinePoint{x: x, y: y})
	}

	return points
}

func pathFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', 1, 64)
}
