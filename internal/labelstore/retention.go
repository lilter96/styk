package labelstore

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// DeleteExpired removes both committed artifacts and abandoned uploads. Expiry
// sorts first in the key, so each bounded sweep makes progress from oldest first.
func (s *Store) DeleteExpired(ctx context.Context, now time.Time, limit int) (int, error) {
	if s == nil {
		return 0, nil
	}
	if limit < 1 || limit > 1000 {
		return 0, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	result, err := s.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(s.bucket), Prefix: aws.String("labels/"), MaxKeys: aws.Int32(int32(limit))})
	if err != nil {
		return 0, ErrUnavailable
	}
	count := 0
	for _, object := range result.Contents {
		key := aws.ToString(object.Key)
		parts := strings.Split(key, "/")
		if len(parts) != 3 {
			continue
		}
		epoch, parseErr := strconv.ParseInt(parts[1], 10, 64)
		if parseErr != nil || !validKey(key, time.Unix(epoch, 0)) || epoch > now.Unix() {
			continue
		}
		_, err = s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.bucket), Key: object.Key})
		if err != nil {
			return count, ErrUnavailable
		}
		count++
	}
	return count, nil
}
