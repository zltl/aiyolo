package console

import (
	"strings"
	"testing"
)

func TestSparklinePathUsesSmoothBezierSegments(t *testing.T) {
	path := sparklinePath([]int64{0, 5, 0, 10})
	want := "M4.0 34.0 C22.7 34.0 22.7 20.0 41.3 20.0 C60.0 20.0 60.0 34.0 78.7 34.0 C97.3 34.0 97.3 6.0 116.0 6.0"
	if path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
}

func TestSparklineAreaPathClosesAgainstBaseline(t *testing.T) {
	path := sparklineAreaPath([]int64{0, 5, 0, 10})
	if !strings.Contains(path, " C") {
		t.Fatalf("expected curved area path, got %q", path)
	}
	if !strings.HasSuffix(path, " L116 34.0 L4 34.0 Z") {
		t.Fatalf("expected baseline closure, got %q", path)
	}
}
