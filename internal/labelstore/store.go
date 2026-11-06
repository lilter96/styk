// Package labelstore keeps encrypted order artifacts in branch-scoped S3 storage.
package labelstore

import (
	"errors"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

var ErrUnavailable = errors.New("encrypted label storage unavailable")

type Store struct {
	client *s3.Client
	bucket string
}

// FromEnvironment is opt-in; installations with inline artifacts keep working.
func FromEnvironment() (*Store, error) {
	bucket := os.Getenv("STYK_LABEL_BUCKET")
	if bucket == "" {
		return nil, nil
	}
	endpoint, err := url.Parse(os.Getenv("AWS_ENDPOINT_URL_S3"))
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil {
		return nil, ErrUnavailable
	}
	region, key, secret := os.Getenv("AWS_REGION"), os.Getenv("AWS_ACCESS_KEY_ID"), os.Getenv("AWS_SECRET_ACCESS_KEY")
	if region == "" || key == "" || secret == "" {
		return nil, ErrUnavailable
	}
	cfg := aws.Config{Region: region, Credentials: credentials.NewStaticCredentialsProvider(key, secret, ""), HTTPClient: &http.Client{Timeout: 10 * time.Second}, RetryMaxAttempts: 3}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) { o.BaseEndpoint = aws.String(endpoint.String()); o.UsePathStyle = true })
	return &Store{client: client, bucket: bucket}, nil
}
