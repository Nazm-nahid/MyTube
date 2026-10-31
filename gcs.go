package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"cloud.google.com/go/storage"
)

// Players keep using the URL for later range requests, so it has to outlast a
// long video.
const streamURLTTL = 6 * time.Hour

var errNotFound = errors.New("video not found")

type Storage struct {
	client *storage.Client
	bucket *storage.BucketHandle
	ttl    time.Duration
}

// NewStorage uses Application Default Credentials.
func NewStorage(ctx context.Context, bucket string, ttl time.Duration) (*Storage, error) {
	client, err := storage.NewClient(ctx)
	if err != nil {
		return nil, err
	}
	return &Storage{client: client, bucket: client.Bucket(bucket), ttl: ttl}, nil
}

func (s *Storage) Close() error {
	return s.client.Close()
}

// SignUpload signs a PUT bound to this key, content type and exact size. The
// client must send the returned headers with the upload, because they are part
// of the signature.
func (s *Storage) SignUpload(key, contentType string, size int64) (string, map[string]string, time.Time, error) {
	expiresAt := time.Now().Add(s.ttl)
	sizeRange := fmt.Sprintf("%d,%d", size, size)
	url, err := s.bucket.SignedURL(key, &storage.SignedURLOptions{
		Scheme:      storage.SigningSchemeV4,
		Method:      "PUT",
		Expires:     expiresAt,
		ContentType: contentType,
		Headers:     []string{"x-goog-content-length-range:" + sizeRange},
	})
	if err != nil {
		return "", nil, time.Time{}, err
	}
	headers := map[string]string{
		"Content-Type":                contentType,
		"x-goog-content-length-range": sizeRange,
	}
	return url, headers, expiresAt, nil
}

// SignRead signs a GET. It does not check that the object exists.
func (s *Storage) SignRead(key string) (string, time.Time, error) {
	expiresAt := time.Now().Add(streamURLTTL)
	url, err := s.bucket.SignedURL(key, &storage.SignedURLOptions{
		Scheme:  storage.SigningSchemeV4,
		Method:  "GET",
		Expires: expiresAt,
	})
	if err != nil {
		return "", time.Time{}, err
	}
	return url, expiresAt, nil
}

func (s *Storage) Exists(ctx context.Context, key string) (bool, error) {
	_, err := s.bucket.Object(key).Attrs(ctx)
	if errors.Is(err, storage.ErrObjectNotExist) {
		return false, nil
	}
	return err == nil, err
}

// Read returns the object's contents, or errNotFound if it does not exist.
func (s *Storage) Read(ctx context.Context, key string) ([]byte, error) {
	r, err := s.bucket.Object(key).NewReader(ctx)
	if errors.Is(err, storage.ErrObjectNotExist) {
		return nil, errNotFound
	}
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}
