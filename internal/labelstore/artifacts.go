package labelstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/lilter96/styk/internal/orders"
)

type reference struct {
	Key string `json:"neon_label_object"`
}

// StoreOrderLabel runs before the completion transaction. Content-addressed PUTs
// are repeatable; an ambiguous PUT is reconciled by reading the expected bytes.
// Unreferenced objects expire through the same bounded retention sweep.
func (s *Store) StoreOrderLabel(ctx context.Context, a orders.LabelArtifact) (orders.LabelArtifact, error) {
	if s == nil {
		return a, nil
	}
	if a.Validate() != nil {
		return a, orders.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	sum := sha256.Sum256(a.Ciphertext)
	key := "labels/" + strconv.FormatInt(a.ExpiresAt.Unix(), 10) + "/" + hex.EncodeToString(sum[:])
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key), Body: bytes.NewReader(a.Ciphertext), ContentType: aws.String("application/octet-stream")})
	if err != nil {
		data, readErr := s.read(ctx, key)
		if readErr != nil || !bytes.Equal(data, a.Ciphertext) {
			return a, ErrUnavailable
		}
	}
	a.Ciphertext, err = json.Marshal(reference{Key: key})
	if err != nil {
		return a, ErrUnavailable
	}
	return a, a.Validate()
}

// LoadOrderLabel is called only after the database authorizes the artifact read.
// Inline artifacts from earlier versions remain readable.
func (s *Store) LoadOrderLabel(ctx context.Context, a orders.LabelArtifact) (orders.LabelArtifact, error) {
	if len(a.Ciphertext) == 0 || a.Ciphertext[0] != '{' {
		return a, nil
	}
	var ref reference
	if s == nil || json.Unmarshal(a.Ciphertext, &ref) != nil || !validKey(ref.Key, a.ExpiresAt) {
		return a, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	data, err := s.read(ctx, ref.Key)
	if err != nil {
		return a, err
	}
	sum := sha256.Sum256(data)
	if !strings.HasSuffix(ref.Key, "/"+hex.EncodeToString(sum[:])) {
		return a, ErrUnavailable
	}
	a.Ciphertext = data
	return a, a.Validate()
}

func validKey(key string, expires time.Time) bool {
	prefix := "labels/" + strconv.FormatInt(expires.Unix(), 10) + "/"
	suffix := strings.TrimPrefix(key, prefix)
	decoded, err := hex.DecodeString(suffix)
	return strings.HasPrefix(key, prefix) && err == nil && len(decoded) == 32 && len(suffix) == 64
}

func (s *Store) read(ctx context.Context, key string) ([]byte, error) {
	result, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		return nil, ErrUnavailable
	}
	data, readErr := io.ReadAll(io.LimitReader(result.Body, (12<<20)+1))
	closeErr := result.Body.Close()
	if readErr != nil || closeErr != nil || len(data) > 12<<20 {
		return nil, ErrUnavailable
	}
	return data, nil
}
