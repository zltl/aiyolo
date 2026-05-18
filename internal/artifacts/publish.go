package artifacts

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/minio/minio-go/v7"
)

type Publisher struct {
	cfg    Config
	client *minio.Client
}

type PublishedObject struct {
	LocalPath string
	ObjectKey string
	PublicURL string
	SHA256    string
	SizeBytes int64
	MediaType string
}

func NewPublisher(cfg Config) (*Publisher, error) {
	if strings.TrimSpace(cfg.S3.Bucket) == "" {
		return nil, fmt.Errorf("artifacts.s3.bucket is required")
	}
	client, err := newMinioClient(cfg.S3)
	if err != nil {
		return nil, err
	}
	return &Publisher{cfg: cfg, client: client}, nil
}

func (publisher *Publisher) UploadFile(ctx context.Context, localPath string, objectKey string) (PublishedObject, error) {
	info, err := os.Stat(localPath)
	if err != nil {
		return PublishedObject{}, err
	}
	key := publisher.cfg.ObjectKey(objectKey)
	if key == "" {
		return PublishedObject{}, fmt.Errorf("artifact object key is required")
	}
	shaValue, err := fileSHA256(localPath)
	if err != nil {
		return PublishedObject{}, err
	}
	mediaType := detectMediaType(localPath)
	_, err = publisher.client.FPutObject(ctx, publisher.cfg.S3.Bucket, key, localPath, minio.PutObjectOptions{ContentType: mediaType, UserMetadata: map[string]string{"sha256": shaValue}})
	if err != nil {
		return PublishedObject{}, err
	}
	return PublishedObject{LocalPath: localPath, ObjectKey: key, PublicURL: publisher.cfg.PublicObjectURL(objectKey), SHA256: shaValue, SizeBytes: info.Size(), MediaType: mediaType}, nil
}

func (publisher *Publisher) UploadBytes(ctx context.Context, payload []byte, objectKey string, mediaType string) (PublishedObject, error) {
	key := publisher.cfg.ObjectKey(objectKey)
	if key == "" {
		return PublishedObject{}, fmt.Errorf("artifact object key is required")
	}
	if mediaType = strings.TrimSpace(mediaType); mediaType == "" {
		mediaType = http.DetectContentType(payload)
	}
	sum := sha256.Sum256(payload)
	shaValue := hex.EncodeToString(sum[:])
	_, err := publisher.client.PutObject(ctx, publisher.cfg.S3.Bucket, key, bytes.NewReader(payload), int64(len(payload)), minio.PutObjectOptions{ContentType: mediaType, UserMetadata: map[string]string{"sha256": shaValue}})
	if err != nil {
		return PublishedObject{}, err
	}
	return PublishedObject{ObjectKey: key, PublicURL: publisher.cfg.PublicObjectURL(objectKey), SHA256: shaValue, SizeBytes: int64(len(payload)), MediaType: mediaType}, nil
}

func (publisher *Publisher) UploadSHA256(ctx context.Context, objectKey string, checksum string) (PublishedObject, error) {
	key := publisher.cfg.ObjectKey(objectKey)
	if key == "" {
		return PublishedObject{}, fmt.Errorf("artifact object key is required")
	}
	content := []byte(strings.TrimSpace(checksum) + "\n")
	_, err := publisher.client.PutObject(ctx, publisher.cfg.S3.Bucket, key, bytes.NewReader(content), int64(len(content)), minio.PutObjectOptions{ContentType: "text/plain; charset=utf-8"})
	if err != nil {
		return PublishedObject{}, err
	}
	return PublishedObject{ObjectKey: key, PublicURL: publisher.cfg.PublicObjectURL(objectKey), SHA256: strings.TrimSpace(checksum), SizeBytes: int64(len(content)), MediaType: "text/plain; charset=utf-8"}, nil
}

func fileSHA256(path string) (string, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func detectMediaType(path string) string {
	if mediaType := mime.TypeByExtension(strings.ToLower(filepath.Ext(path))); mediaType != "" {
		return mediaType
	}
	return "application/octet-stream"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
