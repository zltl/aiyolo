package artifacts

import (
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

func ReleaseObjectKeys(objectKey string, version string) (stable string, latest string, versioned string) {
	stable = NormalizeObjectKey(objectKey)
	version = strings.Trim(strings.TrimSpace(version), "/")
	if stable == "" || version == "" {
		return stable, "", ""
	}
	dir := path.Dir(stable)
	base := path.Base(stable)
	if dir == "." {
		return stable, path.Join("latest", base), path.Join(version, base)
	}
	return stable, path.Join(dir, "latest", base), path.Join(dir, version, base)
}

func ParseReleaseObjectKey(relativeKey string) (platform string, artifactName string, version string, latestAlias bool, stableAlias bool) {
	relativeKey = NormalizeObjectKey(relativeKey)
	if relativeKey == "" {
		return "", "", "", false, false
	}
	parts := strings.Split(relativeKey, "/")
	if len(parts) < 2 {
		return "", path.Base(relativeKey), "", false, true
	}
	platform = parts[0]
	artifactName = parts[len(parts)-1]
	switch len(parts) {
	case 2:
		return platform, artifactName, "", false, true
	default:
		if parts[len(parts)-2] == "latest" {
			return platform, artifactName, "latest", true, false
		}
		return platform, artifactName, parts[len(parts)-2], false, false
	}
}

type ReleaseView struct {
	RelativeKey  string
	DownloadPath string
	SHA256Path   string
	Version      string
	SizeLabel    string
	UpdatedAt    time.Time
	Latest       bool
	Stable       bool
}

func BuildReleaseViews(basePath string, entries []CatalogEntry, platform string, artifactName string) []ReleaseView {
	items := make([]ReleaseView, 0, len(entries))
	for _, entry := range entries {
		if entry.Platform != platform || entry.ArtifactName != artifactName || strings.HasSuffix(entry.RelativeKey, ".sha256") {
			continue
		}
		items = append(items, ReleaseView{RelativeKey: entry.RelativeKey, DownloadPath: path.Join(basePath, entry.RelativeKey), SHA256Path: path.Join(basePath, entry.RelativeKey+".sha256"), Version: entry.Version, SizeLabel: HumanSize(entry.SizeBytes), UpdatedAt: entry.LastModified, Latest: entry.LatestAlias, Stable: entry.StableAlias})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Stable != items[j].Stable {
			return items[i].Stable
		}
		if items[i].Latest != items[j].Latest {
			return items[i].Latest
		}
		if items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].RelativeKey < items[j].RelativeKey
		}
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	return items
}

func HumanSize(sizeBytes int64) string {
	const unit = 1024
	if sizeBytes < unit {
		return strconv.FormatInt(sizeBytes, 10) + " B"
	}
	div, exp := int64(unit), 0
	for value := sizeBytes / unit; value >= unit; value /= unit {
		div *= unit
		exp++
	}
	return strconv.FormatFloat(float64(sizeBytes)/float64(div), 'f', 1, 64) + " " + string("KMGTPE"[exp]) + "iB"
}
