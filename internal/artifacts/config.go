package artifacts

import (
	"net/url"
	"path"
	"strings"
)

type Config struct {
	PublicBaseURL string
	ProxyBasePath string
	PublicViaProxy bool
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

func (cfg Config) RelativeObjectKey(objectKey string) string {
	cleaned := NormalizeObjectKey(objectKey)
	if cleaned == "" {
		return ""
	}
	prefix := NormalizeObjectKey(cfg.S3.Prefix)
	if prefix == "" {
		return cleaned
	}
	if cleaned == prefix {
		return ""
	}
	if strings.HasPrefix(cleaned, prefix+"/") {
		return strings.TrimPrefix(cleaned, prefix+"/")
	}
	return cleaned
}

func (cfg Config) PublicObjectURL(objectKey string) string {
	if cfg.PublicViaProxy {
		base := normalizeURLLike(cfg.PublicBaseURL)
		key := cfg.ProxyObjectURL(objectKey)
		if base == "" || key == "" {
			return ""
		}
		return base + key
	}
	base := cfg.PublicBase()
	key := cfg.ObjectKey(objectKey)
	if base == "" || key == "" {
		return ""
	}
	return base + "/" + key
}

func (cfg Config) ProxyObjectURL(objectKey string) string {
	key := cfg.RelativeObjectKey(objectKey)
	if key == "" {
		return ""
	}
	return path.Join(cfg.NormalizedProxyBasePath(), key)
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
