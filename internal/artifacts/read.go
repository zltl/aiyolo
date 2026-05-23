package artifacts

import (
	"context"
	"io"
	"strings"

	"github.com/minio/minio-go/v7"
)

type ObjectReader struct {
	cfg    Config
	client *minio.Client
}

func NewObjectReader(cfg Config) (*ObjectReader, error) {
	client, err := newMinioClient(cfg.S3)
	if err != nil {
		return nil, err
	}
	return &ObjectReader{cfg: cfg, client: client}, nil
}

func (reader *ObjectReader) ReadObject(ctx context.Context, objectKey string) ([]byte, string, error) {
	key := reader.cfg.ObjectKey(objectKey)
	object, err := reader.client.GetObject(ctx, reader.cfg.S3.Bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, "", err
	}
	defer object.Close()
	info, err := object.Stat()
	if err != nil {
		return nil, "", err
	}
	payload, err := io.ReadAll(object)
	if err != nil {
		return nil, "", err
	}
	mediaType := strings.TrimSpace(info.ContentType)
	if mediaType == "" {
		mediaType = detectMediaType(objectKey)
	}
	return payload, mediaType, nil
}
