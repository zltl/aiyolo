package artifacts

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/minio/minio-go/v7"
	miniocreds "github.com/minio/minio-go/v7/pkg/credentials"
)

func newMinioClient(cfg S3Config) (*minio.Client, error) {
	endpoint := cfg.ActiveEndpoint()
	if endpoint == "" {
		return nil, fmt.Errorf("artifacts.s3.endpoint is required")
	}
	if strings.TrimSpace(cfg.AccessKeyID) == "" || strings.TrimSpace(cfg.AccessKeySecret) == "" {
		return nil, fmt.Errorf("artifacts.s3 access key is required")
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(parsed.Host) == "" {
		return nil, fmt.Errorf("invalid artifacts.s3 endpoint")
	}
	return minio.New(parsed.Host, &minio.Options{Creds: miniocreds.NewStaticV4(cfg.AccessKeyID, cfg.AccessKeySecret, ""), Secure: parsed.Scheme == "https", Region: firstNonEmpty(cfg.Region, "cn-guangzhou")})
}

func (cfg Config) CanList() bool {
	return strings.TrimSpace(cfg.S3.Bucket) != "" && strings.TrimSpace(cfg.S3.ActiveEndpoint()) != "" && strings.TrimSpace(cfg.S3.AccessKeyID) != "" && strings.TrimSpace(cfg.S3.AccessKeySecret) != ""
}
