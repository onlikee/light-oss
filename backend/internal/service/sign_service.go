package service

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	apperrors "light-oss/backend/internal/pkg/errors"
	"light-oss/backend/internal/signing"
)

type SignService struct {
	signer     *signing.Signer
	defaultTTL int64
	maxTTL     int64
}

func NewSignService(signer *signing.Signer, defaultTTL int64, maxTTL int64) *SignService {
	return &SignService{
		signer:     signer,
		defaultTTL: defaultTTL,
		maxTTL:     maxTTL,
	}
}

func (s *SignService) GenerateDownloadPath(bucketName string, objectKey string, expiresInSeconds int64) (string, int64, error) {
	if err := ValidateBucketName(bucketName); err != nil {
		return "", 0, err
	}
	if err := ValidateObjectKey(objectKey); err != nil {
		return "", 0, err
	}

	if expiresInSeconds <= 0 {
		expiresInSeconds = s.defaultTTL
	}
	if expiresInSeconds > s.maxTTL {
		return "", 0, apperrors.New(http.StatusBadRequest, "invalid_expiry", "expires_in_seconds exceeds maximum")
	}

	expiresAt := time.Now().UTC().Add(time.Duration(expiresInSeconds) * time.Second).Unix()
	signature := s.signer.SignDownload(bucketName, objectKey, expiresAt)

	path := fmt.Sprintf("/api/v1/buckets/%s/objects/%s", bucketName, escapeObjectKey(objectKey))
	values := url.Values{}
	values.Set("expires", fmt.Sprintf("%d", expiresAt))
	values.Set("signature", signature)

	return path + "?" + values.Encode(), expiresAt, nil
}

func (s *SignService) VerifyDownload(bucketName string, objectKey string, expiresAt int64, signature string) error {
	if expiresAt <= 0 || signature == "" {
		return apperrors.New(http.StatusUnauthorized, "invalid_signature", "signature is invalid")
	}
	if time.Now().UTC().Unix() > expiresAt {
		return apperrors.New(http.StatusUnauthorized, "signature_expired", "signature has expired")
	}
	if !s.signer.VerifyDownload("GET", bucketName, objectKey, expiresAt, signature) {
		return apperrors.New(http.StatusUnauthorized, "invalid_signature", "signature is invalid")
	}

	return nil
}

func escapeObjectKey(objectKey string) string {
	parts := strings.Split(objectKey, "/")
	escaped := make([]string, 0, len(parts))
	for _, part := range parts {
		escaped = append(escaped, url.PathEscape(part))
	}

	return strings.Join(escaped, "/")
}
