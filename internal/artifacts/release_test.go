package artifacts

import (
	"testing"
	"time"
)

func TestReleaseObjectKeys(t *testing.T) {
	stable, latest, versioned := ReleaseObjectKeys("windows/aiyolo.exe", "v0.1.0")
	if stable != "windows/aiyolo.exe" {
		t.Fatalf("stable = %q, want %q", stable, "windows/aiyolo.exe")
	}
	if latest != "windows/latest/aiyolo.exe" {
		t.Fatalf("latest = %q, want %q", latest, "windows/latest/aiyolo.exe")
	}
	if versioned != "windows/v0.1.0/aiyolo.exe" {
		t.Fatalf("versioned = %q, want %q", versioned, "windows/v0.1.0/aiyolo.exe")
	}
}

func TestBuildReleaseViewsOrdersStableLatestThenVersioned(t *testing.T) {
	now := time.Date(2025, time.January, 2, 3, 4, 5, 0, time.UTC)
	entries := []CatalogEntry{
		DescribeCatalogEntry(Config{}, "windows/v0.1.0/aiyolo.exe", 2048, now.Add(-2*time.Hour)),
		DescribeCatalogEntry(Config{}, "windows/latest/aiyolo.exe", 2048, now.Add(-1*time.Hour)),
		DescribeCatalogEntry(Config{}, "windows/aiyolo.exe", 2048, now),
		DescribeCatalogEntry(Config{}, "gateway/linux-amd64/aiyolo-gateway", 4096, now),
	}
	views := BuildReleaseViews("/artifacts", entries, "windows", "aiyolo.exe")
	if len(views) != 3 {
		t.Fatalf("len(views) = %d, want 3", len(views))
	}
	if views[0].RelativeKey != "windows/aiyolo.exe" || !views[0].Stable {
		t.Fatalf("views[0] = %+v, want stable alias first", views[0])
	}
	if views[1].RelativeKey != "windows/latest/aiyolo.exe" || !views[1].Latest {
		t.Fatalf("views[1] = %+v, want latest alias second", views[1])
	}
	if views[2].RelativeKey != "windows/v0.1.0/aiyolo.exe" || views[2].Version != "v0.1.0" {
		t.Fatalf("views[2] = %+v, want versioned object third", views[2])
	}
	if views[2].DownloadPath != "/artifacts/windows/v0.1.0/aiyolo.exe" {
		t.Fatalf("download path = %q, want %q", views[2].DownloadPath, "/artifacts/windows/v0.1.0/aiyolo.exe")
	}
	if views[2].SHA256Path != "/artifacts/windows/v0.1.0/aiyolo.exe.sha256" {
		t.Fatalf("sha256 path = %q, want %q", views[2].SHA256Path, "/artifacts/windows/v0.1.0/aiyolo.exe.sha256")
	}
}
