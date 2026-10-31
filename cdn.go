package main

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// CDN signs Cloud CDN URLs. Its signature parameters are left out of the cache
// key, so every viewer shares the same cached object.
type CDN struct {
	baseURL string
	keyName string
	key     []byte
}

// NewCDN returns nil when no base URL is set, so reads stay on signed GCS URLs.
// key is the URL-safe base64 signing key added to the backend bucket.
func NewCDN(baseURL, keyName, key string) (*CDN, error) {
	if baseURL == "" {
		return nil, nil
	}
	if keyName == "" || key == "" {
		return nil, errors.New("CDN_KEY_NAME and CDN_KEY are required with CDN_URL")
	}
	decoded, err := base64.URLEncoding.DecodeString(key)
	if err != nil {
		return nil, fmt.Errorf("decode CDN_KEY: %w", err)
	}
	return &CDN{baseURL: strings.TrimRight(baseURL, "/"), keyName: keyName, key: decoded}, nil
}

// Sign covers the whole URL, so the path and expiry cannot be changed.
func (c *CDN) Sign(key string, expiresAt time.Time) string {
	path := (&url.URL{Path: "/" + key}).EscapedPath()
	unsigned := fmt.Sprintf("%s%s?Expires=%d&KeyName=%s", c.baseURL, path, expiresAt.Unix(), c.keyName)
	mac := hmac.New(sha1.New, c.key)
	mac.Write([]byte(unsigned))
	return unsigned + "&Signature=" + base64.URLEncoding.EncodeToString(mac.Sum(nil))
}
