package artifacts

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
)

type Catalog struct {
	GeneratedAt time.Time      `json:"generated_at"`
	Entries     []CatalogEntry `json:"entries"`
}

type CatalogEntry struct {
	ObjectKey    string    `json:"object_key"`
	RelativeKey  string    `json:"relative_key"`
	PublicURL    string    `json:"public_url"`
	SizeBytes    int64     `json:"size_bytes"`
	LastModified time.Time `json:"last_modified"`
	SHA256URL    string    `json:"sha256_url,omitempty"`
	MediaType    string    `json:"media_type,omitempty"`
	StableAlias  bool      `json:"stable_alias,omitempty"`
	LatestAlias  bool      `json:"latest_alias,omitempty"`
	Version      string    `json:"version,omitempty"`
	Platform     string    `json:"platform,omitempty"`
	ArtifactName string    `json:"artifact_name,omitempty"`
}

type CatalogReader interface {
	Catalog(ctx context.Context, prefix string) (Catalog, error)
}

type S3CatalogReader struct {
	cfg    Config
	client *minio.Client
}

func NewCatalogReader(cfg Config) (*S3CatalogReader, error) {
	client, err := newMinioClient(cfg.S3)
	if err != nil {
		return nil, err
	}
	return &S3CatalogReader{cfg: cfg, client: client}, nil
}

func (reader *S3CatalogReader) Catalog(ctx context.Context, prefix string) (Catalog, error) {
	prefix = reader.cfg.ObjectKey(prefix)
	entries := make([]CatalogEntry, 0, 32)
	for object := range reader.client.ListObjects(ctx, reader.cfg.S3.Bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if object.Err != nil {
			return Catalog{}, object.Err
		}
		relativeKey := strings.TrimPrefix(object.Key, NormalizeObjectKey(reader.cfg.S3.Prefix))
		relativeKey = strings.TrimPrefix(relativeKey, "/")
		entry := DescribeCatalogEntry(reader.cfg, relativeKey, object.Size, object.LastModified)
		if entry.ObjectKey == "" {
			entry.ObjectKey = object.Key
		}
		if entry.PublicURL == "" {
			entry.PublicURL = reader.cfg.PublicObjectURL(relativeKey)
		}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].LastModified.Equal(entries[j].LastModified) {
			return entries[i].RelativeKey < entries[j].RelativeKey
		}
		return entries[i].LastModified.After(entries[j].LastModified)
	})
	return Catalog{GeneratedAt: time.Now().UTC(), Entries: entries}, nil
}

func CatalogHandler(cfg Config) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !cfg.CanList() {
			http.Error(w, "artifact catalog is not configured", http.StatusServiceUnavailable)
			return
		}
		reader, err := NewCatalogReader(cfg)
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		catalog, err := reader.Catalog(r.Context(), "")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(catalog)
	})
}

func DescribeCatalogEntry(cfg Config, relativeKey string, sizeBytes int64, lastModified time.Time) CatalogEntry {
	relativeKey = NormalizeObjectKey(relativeKey)
	entry := CatalogEntry{ObjectKey: cfg.ObjectKey(relativeKey), RelativeKey: relativeKey, PublicURL: cfg.PublicObjectURL(relativeKey), SizeBytes: sizeBytes, LastModified: lastModified, MediaType: detectMediaType(relativeKey)}
	if strings.HasSuffix(relativeKey, ".sha256") {
		return entry
	}
	entry.SHA256URL = cfg.PublicObjectURL(relativeKey + ".sha256")
	platform, artifactName, version, latestAlias, stableAlias := ParseReleaseObjectKey(relativeKey)
	entry.Platform = platform
	entry.ArtifactName = artifactName
	entry.Version = version
	entry.LatestAlias = latestAlias
	entry.StableAlias = stableAlias
	return entry
}
