package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
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
		Region: input.Region, Credentials: credentialsProvider, HTTPClient: &http.Client{Timeout: 45 * time.Second},
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
	if !safeS3Key(key) {
		return ErrInvalidKey
	}
	digest := sha256.Sum256(data)
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.bucket), Key: aws.String(key), Body: bytes.NewReader(data),
		ContentLength: aws.Int64(int64(len(data))), ContentType: aws.String(contentType),
		ChecksumAlgorithm: types.ChecksumAlgorithmSha256, ChecksumSHA256: aws.String(base64.StdEncoding.EncodeToString(digest[:])),
		IfNoneMatch: aws.String("*"), ServerSideEncryption: types.ServerSideEncryptionAes256,
		Metadata: map[string]string{"hcai-sha256": fmt.Sprintf("%x", digest[:])},
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
		return ObjectInfo{}, ErrNotFound
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
		return Object{}, ErrNotFound
	}
	if err != nil {
		return Object{}, fmt.Errorf("open S3 media object: %w", err)
	}
	size := aws.ToInt64(result.ContentLength)
	if requested != nil {
		size = requested.End - requested.Start + 1
	}
	return Object{Body: result.Body, Info: ObjectInfo{Size: size, LastModified: aws.ToTime(result.LastModified).UTC(), ETag: aws.ToString(result.ETag)}}, nil
}

func (s *S3Store) Delete(ctx context.Context, key string) error {
	if !safeS3Key(key) {
		return ErrInvalidKey
	}
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil && !isHTTPStatus(err, http.StatusNotFound) {
		return fmt.Errorf("delete S3 media object: %w", err)
	}
	return nil
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
