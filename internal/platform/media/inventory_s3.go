package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

func (s *S3Store) InventoryScope() InventoryScope {
	digest := sha256.Sum256([]byte(aws.ToString(s.client.Options().BaseEndpoint)))
	prefix := s.prefix
	if prefix != "" {
		prefix += "/"
	}
	return InventoryScope{Backend: s.Backend(), Location: s.bucket, Prefix: prefix, EndpointSHA256: hex.EncodeToString(digest[:])}
}

// Bound XML before the SDK decodes it, including unsuccessful responses.
type inventoryHTTPClient struct{ client s3.HTTPClient }

func (c inventoryHTTPClient) Do(request *http.Request) (*http.Response, error) {
	response, err := c.client.Do(request)
	if err != nil {
		return nil, err
	}
	const maximum = 4 << 20
	body, readErr := io.ReadAll(io.LimitReader(response.Body, maximum+1))
	closeErr := response.Body.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if len(body) > maximum {
		return nil, ErrIntegrity
	}
	response.Body = io.NopCloser(bytes.NewReader(body))
	return response, nil
}

// WalkInventory supports the ordered ListObjectsV2 contract of general-purpose
// S3 buckets. Version history, delete markers and unfinished multipart uploads
// are not current objects and are outside this inventory's scope.
func (s *S3Store) WalkInventory(ctx context.Context, limit int, visit func([]InventoryEntry) error) error {
	if err := validInventoryLimit(limit); err != nil {
		return err
	}
	prefix := s.InventoryScope().Prefix
	var token *string
	last := ""
	count := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		maximum := min(InventoryBatchSize, limit-count+1)
		page, err := s.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket: aws.String(s.bucket), Prefix: aws.String(prefix), MaxKeys: aws.Int32(int32(maximum)),
			ContinuationToken: token, EncodingType: types.EncodingTypeUrl,
		}, func(o *s3.Options) { o.HTTPClient = inventoryHTTPClient{o.HTTPClient} })
		if err != nil {
			return err
		}
		if page.IsTruncated == nil || page.EncodingType != types.EncodingTypeUrl ||
			page.KeyCount == nil || int(*page.KeyCount) != len(page.Contents) || len(page.Contents) > maximum ||
			len(page.CommonPrefixes) != 0 || aws.ToString(page.Name) != s.bucket {
			return ErrIntegrity
		}
		returnedPrefix, err := url.PathUnescape(aws.ToString(page.Prefix))
		if err != nil || returnedPrefix != prefix {
			return ErrIntegrity
		}
		entries := make([]InventoryEntry, 0, len(page.Contents))
		for _, object := range page.Contents {
			key, err := url.PathUnescape(aws.ToString(object.Key))
			if err != nil || !utf8.ValidString(key) || len(key) == 0 || len(key) > 1024 ||
				!strings.HasPrefix(key, prefix) || key <= last || object.Size == nil || *object.Size < 0 {
				return ErrIntegrity
			}
			last = key
			count++
			if count > limit {
				return ErrInventoryLimit
			}
			kind := "object"
			if !safeS3Key(key) {
				kind = "invalid_key"
			}
			entries = append(entries, InventoryEntry{Key: key, Kind: kind, Size: *object.Size})
		}
		if len(entries) > 0 {
			if err := visit(entries); err != nil {
				return err
			}
		}
		if !*page.IsTruncated {
			return ctx.Err()
		}
		next := aws.ToString(page.NextContinuationToken)
		if len(entries) == 0 || len(next) == 0 || len(next) > 8192 || next == aws.ToString(token) {
			return ErrIntegrity
		}
		if count == limit {
			return ErrInventoryLimit
		}
		token = aws.String(next)
	}
}
