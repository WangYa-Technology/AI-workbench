package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

type S3Config struct {
	Bucket          string
	Region          string
	Endpoint        string
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
	PathStyle       bool
	Prefix          string
}

type S3Store struct {
	bucket string
	prefix string
	client *s3.Client
}

func NewS3Store(input S3Config) *S3Store {
	credentialsProvider := aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(input.AccessKeyID, input.SecretAccessKey, input.SessionToken))
	client := s3.NewFromConfig(aws.Config{
		Region: input.Region, Credentials: credentialsProvider, HTTPClient: &http.Client{
			Timeout: 45 * time.Second,
			// The configured bucket/endpoint is the storage boundary. Following
			// redirects can change a signed write into a GET, report a false
			// success, or expose private object requests to a different target.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		RetryMaxAttempts: 3, RetryMode: aws.RetryModeStandard,
	}, func(options *s3.Options) {
		options.UsePathStyle = input.PathStyle
		options.RetryMaxAttempts = 3
		if input.Endpoint != "" {
			options.BaseEndpoint = aws.String(input.Endpoint)
		}
	})
	return &S3Store{bucket: input.Bucket, prefix: strings.Trim(input.Prefix, "/"), client: client}
}

func (s *S3Store) Backend() string { return "s3" }

func (s *S3Store) ObjectKey(base string) (string, error) {
	if !safeS3Key(base) || strings.Contains(base, "/") {
		return "", ErrInvalidKey
	}
	if s.prefix == "" {
		return base, nil
	}
	return s.prefix + "/" + base, nil
}

func (s *S3Store) Put(ctx context.Context, key string, data []byte, contentType string) error {
	digest := sha256.Sum256(data)
	return s.PutStream(ctx, key, bytes.NewReader(data), int64(len(data)), hex.EncodeToString(digest[:]), contentType)
}

func (s *S3Store) PutStream(ctx context.Context, key string, body io.ReadSeeker, size int64, digest, contentType string) error {
	if !safeS3Key(key) {
		return ErrInvalidKey
	}
	checksum, err := hex.DecodeString(digest)
	if err != nil || len(checksum) != sha256.Size || size < 0 {
		return ErrIntegrity
	}
	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.bucket), Key: aws.String(key), Body: body,
		ContentLength: aws.Int64(size), ContentType: aws.String(contentType),
		ChecksumAlgorithm: types.ChecksumAlgorithmSha256, ChecksumSHA256: aws.String(base64.StdEncoding.EncodeToString(checksum)),
		IfNoneMatch: aws.String("*"), ServerSideEncryption: types.ServerSideEncryptionAes256,
		Metadata: map[string]string{"hcai-sha256": digest},
	})
	if isHTTPStatus(err, http.StatusConflict, http.StatusPreconditionFailed) {
		return ErrConflict
	}
	if err != nil {
		return fmt.Errorf("put S3 media object: %w", err)
	}
	return nil
}

func (s *S3Store) Stat(ctx context.Context, key string) (ObjectInfo, error) {
	if !safeS3Key(key) {
		return ObjectInfo{}, ErrInvalidKey
	}
	result, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if isHTTPStatus(err, http.StatusNotFound) {
		return ObjectInfo{}, s.objectMissing(ctx)
	}
	if err != nil {
		return ObjectInfo{}, fmt.Errorf("stat S3 media object: %w", err)
	}
	return ObjectInfo{Size: aws.ToInt64(result.ContentLength), LastModified: aws.ToTime(result.LastModified).UTC(), ETag: aws.ToString(result.ETag)}, nil
}

func (s *S3Store) Open(ctx context.Context, key string, requested *ByteRange) (Object, error) {
	if !safeS3Key(key) {
		return Object{}, ErrInvalidKey
	}
	input := &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key), ChecksumMode: types.ChecksumModeEnabled}
	if requested != nil {
		if requested.Start < 0 || requested.End < requested.Start {
			return Object{}, ErrInvalidKey
		}
		input.Range = aws.String(fmt.Sprintf("bytes=%d-%d", requested.Start, requested.End))
	}
	result, err := s.client.GetObject(ctx, input)
	if isHTTPStatus(err, http.StatusNotFound) {
		return Object{}, s.objectMissing(ctx)
	}
	if err != nil {
		return Object{}, fmt.Errorf("open S3 media object: %w", err)
	}
	size, err := s3ReadSize(result, requested)
	if err != nil {
		if result.Body != nil {
			_ = result.Body.Close()
		}
		return Object{}, err
	}
	return Object{Body: result.Body, Info: ObjectInfo{Size: size, LastModified: aws.ToTime(result.LastModified).UTC(), ETag: aws.ToString(result.ETag)}}, nil
}

// A successful SDK call alone does not prove that a compatible endpoint
// honored Range. Validate before exposing its body; report the full object's
// size consistently with LocalStore and verified delivery reads.
func s3ReadSize(result *s3.GetObjectOutput, requested *ByteRange) (int64, error) {
	response, ok := awsmiddleware.GetRawResponse(result.ResultMetadata).(*smithyhttp.Response)
	if !ok || response == nil || response.Response == nil || result.Body == nil || result.ContentLength == nil || *result.ContentLength < 0 {
		return 0, ErrIntegrity
	}
	if requested == nil {
		if response.StatusCode != http.StatusOK || aws.ToString(result.ContentRange) != "" {
			return 0, ErrIntegrity
		}
		return *result.ContentLength, nil
	}
	prefix := fmt.Sprintf("bytes %d-%d/", requested.Start, requested.End)
	value := aws.ToString(result.ContentRange)
	if response.StatusCode != http.StatusPartialContent || !strings.HasPrefix(value, prefix) {
		return 0, ErrIntegrity
	}
	totalText := strings.TrimPrefix(value, prefix)
	total, err := strconv.ParseInt(totalText, 10, 64)
	if err != nil || total <= requested.End || strconv.FormatInt(total, 10) != totalText || *result.ContentLength != requested.End-requested.Start+1 {
		return 0, ErrIntegrity
	}
	return total, nil
}

func (s *S3Store) Delete(ctx context.Context, key string) error {
	if !safeS3Key(key) {
		return ErrInvalidKey
	}
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if isHTTPStatus(err, http.StatusNotFound) {
		err = s.objectMissing(ctx)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
	}
	if err != nil {
		return fmt.Errorf("delete S3 media object: %w", err)
	}
	return nil
}

// HeadObject has no error body: a 404 can mean the bucket itself is missing.
// Verify the configured bucket with the same signed client before returning
// object absence to delivery, recovery or cleanup. Do not cache this check:
// a prior healthy bucket is not evidence about the current missing read.
func (s *S3Store) objectMissing(ctx context.Context) error {
	_, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(s.bucket)})
	if err != nil {
		return errors.Join(ErrStorageUnavailable, fmt.Errorf("verify S3 media bucket: %w", err))
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return ErrNotFound
}

func safeS3Key(key string) bool {
	if key == "" || len(key) > 1024 || strings.HasPrefix(key, "/") || strings.HasSuffix(key, "/") || strings.ContainsAny(key, "\\\x00\r\n") {
		return false
	}
	for _, segment := range strings.Split(key, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func isHTTPStatus(err error, statuses ...int) bool {
	if err == nil {
		return false
	}
	var responseError *smithyhttp.ResponseError
	if !errors.As(err, &responseError) {
		return false
	}
	for _, status := range statuses {
		if responseError.HTTPStatusCode() == status {
			return true
		}
	}
	return false
}

var _ io.ReadCloser = (*limitedReadCloser)(nil)
