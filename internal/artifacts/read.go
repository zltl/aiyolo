package artifacts

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
)

type ObjectStream struct {
	Body          io.ReadCloser
	ContentType   string
	ContentLength int64
	ETag          string
	LastModified  time.Time
}

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

func (reader *ObjectReader) OpenObject(ctx context.Context, objectKey string) (ObjectStream, error) {
	key := reader.cfg.ObjectKey(objectKey)
	if key == "" {
		return ObjectStream{}, minio.ErrorResponse{StatusCode: http.StatusNotFound, Code: "NoSuchKey", Message: "artifact path is required"}
	}
	object, err := reader.client.GetObject(ctx, reader.cfg.S3.Bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return ObjectStream{}, err
	}
	info, err := object.Stat()
	if err != nil {
		_ = object.Close()
		return ObjectStream{}, err
	}
	mediaType := strings.TrimSpace(info.ContentType)
	if mediaType == "" {
		mediaType = detectMediaType(objectKey)
	}
	return ObjectStream{
		Body:          object,
		ContentType:   mediaType,
		ContentLength: info.Size,
		ETag:          strings.Trim(strings.TrimSpace(info.ETag), "\""),
		LastModified:  info.LastModified.UTC(),
	}, nil
}

func (reader *ObjectReader) ReadObject(ctx context.Context, objectKey string) ([]byte, string, error) {
	stream, err := reader.OpenObject(ctx, objectKey)
	if err != nil {
		return nil, "", err
	}
	defer stream.Body.Close()
	payload, err := io.ReadAll(stream.Body)
	if err != nil {
		return nil, "", err
	}
	return payload, stream.ContentType, nil
}

func isObjectNotFound(err error) bool {
	response := minio.ToErrorResponse(err)
	return response.Code == "NoSuchKey" || response.Code == "NoSuchBucket" || response.StatusCode == http.StatusNotFound
}
