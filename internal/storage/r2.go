// Package storage uploads objects to Cloudflare R2 using S3-compatible
// SigV4 auth. Only PutObject is implemented — that's all the agent needs.
package storage

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type R2 struct {
	AccountID  string
	Bucket     string
	AccessKey  string
	SecretKey  string
	PublicBase string // optional; e.g. https://pub-xxx.r2.dev or a custom domain
	HTTP       *http.Client
}

func New(accountID, bucket, accessKey, secretKey, publicBase string) *R2 {
	return &R2{
		AccountID:  accountID,
		Bucket:     bucket,
		AccessKey:  accessKey,
		SecretKey:  secretKey,
		PublicBase: publicBase,
		HTTP:       &http.Client{Timeout: 60 * time.Second},
	}
}

// Put uploads data to <bucket>/<key>. Returns a URL pointing at the object:
// the public URL when PublicBase is set, otherwise an opaque "r2://" ref.
// key must be path-safe (callers should pre-encode or use simple ASCII keys).
func (r *R2) Put(ctx context.Context, key string, data []byte, contentType string) (string, error) {
	key = strings.TrimPrefix(key, "/")
	host := r.AccountID + ".r2.cloudflarestorage.com"
	canonicalURI := "/" + r.Bucket + "/" + key
	endpoint := "https://" + host + canonicalURI

	payloadHash := sha256Hex(data)
	now := time.Now().UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")
	region := "auto"
	service := "s3"

	req, err := http.NewRequestWithContext(ctx, "PUT", endpoint, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Host", host)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	req.Header.Set("X-Amz-Date", amzDate)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.ContentLength = int64(len(data))

	signedHeaders := "host;x-amz-content-sha256;x-amz-date"
	canonicalHeaders := fmt.Sprintf(
		"host:%s\nx-amz-content-sha256:%s\nx-amz-date:%s\n",
		host, payloadHash, amzDate,
	)

	canonicalRequest := strings.Join([]string{
		"PUT",
		canonicalURI,
		"", // empty query
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")

	credScope := dateStamp + "/" + region + "/" + service + "/aws4_request"
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		credScope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")

	signingKey := deriveKey(r.SecretKey, dateStamp, region, service)
	signature := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))

	req.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		r.AccessKey, credScope, signedHeaders, signature,
	))

	resp, err := r.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("r2 put %d: %s", resp.StatusCode, string(body))
	}

	if r.PublicBase != "" {
		return strings.TrimRight(r.PublicBase, "/") + "/" + key, nil
	}
	return fmt.Sprintf("r2://%s/%s", r.Bucket, key), nil
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func hmacSHA256(key, data []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(data)
	return m.Sum(nil)
}

func deriveKey(secret, date, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secret), []byte(date))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	return hmacSHA256(kService, []byte("aws4_request"))
}
