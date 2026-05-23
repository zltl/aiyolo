package artifacts

import (
	"fmt"
	"net/url"
	"path"
	"strings"
)

type Config struct {
	PublicBaseURL string
	ProxyBasePath string
	S3            S3Config
}

type S3Config struct {
	Endpoint         string
	InternalEndpoint string
	Region           string
	Bucket           string
	Prefix           string
	AccessKeyID      string
	AccessKeySecret  string
	BucketDomain     string
	CNAMEDomain      string
	UseInternal      bool
}

func (cfg Config) Enabled() bool {
	return cfg.PublicBase() != ""
}

func (cfg Config) CanUpload() bool {
	return cfg.CanList() && cfg.PublicBase() != ""
}

func (cfg Config) PublicBase() string {
	for _, candidate := range []string{cfg.PublicBaseURL, cfg.S3.CNAMEDomain, cfg.S3.BucketDomain} {
		if normalized := normalizeURLLike(candidate); normalized != "" {
			return normalized
		}
	}
	return ""
}

func (cfg Config) NormalizedProxyBasePath() string {
	base := strings.TrimSpace(cfg.ProxyBasePath)
	if base == "" {
		base = "/artifacts"
	}
	if !strings.HasPrefix(base, "/") {
		base = "/" + base
	}
	base = path.Clean(base)
	if base == "." {
		return "/artifacts"
	}
	return strings.TrimRight(base, "/")
}

func (cfg Config) ObjectKey(objectKey string) string {
	cleaned := NormalizeObjectKey(objectKey)
	if cleaned == "" {
		return ""
	}
	return cfg.S3.ObjectKey(cleaned)
}

func (cfg Config) PublicObjectURL(objectKey string) string {
	base := cfg.PublicBase()
	key := cfg.ObjectKey(objectKey)
	if base == "" || key == "" {
		return ""
	}
	return base + "/" + key
}

func (cfg Config) ProxyObjectURL(objectKey string) string {
	key := NormalizeObjectKey(objectKey)
	if key == "" {
		return ""
	}
	return path.Join(cfg.NormalizedProxyBasePath(), key)
}

func (cfg Config) ResolveProxyTarget(requestPath string) (string, error) {
	key := cfg.ObjectKey(requestPath)
	if key == "" {
		return "", fmt.Errorf("artifact path is required")
	}
	base := cfg.PublicBase()
	if base == "" {
		return "", fmt.Errorf("artifact public base URL is not configured")
	}
	return base + "/" + key, nil
}

func (cfg S3Config) ActiveEndpoint() string {
	if cfg.UseInternal {
		if normalized := normalizeURLLike(cfg.InternalEndpoint); normalized != "" {
			return normalized
		}
	}
	return normalizeURLLike(cfg.Endpoint)
}

func (cfg S3Config) ObjectKey(objectKey string) string {
	key := NormalizeObjectKey(objectKey)
	if key == "" {
		return ""
	}
	prefix := NormalizeObjectKey(cfg.Prefix)
	if prefix == "" {
		return key
	}
	if key == prefix || strings.HasPrefix(key, prefix+"/") {
		return key
	}
	return prefix + "/" + key
}

func NormalizeObjectKey(value string) string {
	cleaned := path.Clean("/" + strings.TrimSpace(value))
	cleaned = strings.TrimPrefix(cleaned, "/")
	if cleaned == "." {
		return ""
	}
	return cleaned
}

func normalizeURLLike(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	if !strings.Contains(trimmed, "://") {
		trimmed = "https://" + trimmed
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || strings.TrimSpace(parsed.Host) == "" {
		return ""
	}
	parsed.Path = strings.TrimRight(parsed.EscapedPath(), "/")
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return strings.TrimRight(parsed.String(), "/")
}
