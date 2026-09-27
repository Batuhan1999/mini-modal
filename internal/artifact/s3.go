package artifact

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

var ErrNotFound = errors.New("artifact not found")
var ErrStorage = errors.New("artifact storage unavailable")

type Config struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	Bucket    string
}

type UploadResult struct {
	Key       string `json:"artifact_key"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
}

type S3Store struct {
	client *s3.Client
	bucket string
}

func NewS3Store(ctx context.Context, settings Config) (*S3Store, error) {
	if settings.Endpoint == "" || settings.AccessKey == "" || settings.SecretKey == "" || settings.Bucket == "" {
		return nil, fmt.Errorf("S3 endpoint, access key, secret key, and bucket are required")
	}
	endpoint, err := url.Parse(settings.Endpoint)
	if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return nil, fmt.Errorf("invalid S3 endpoint %q", settings.Endpoint)
	}
	awsConfig, err := config.LoadDefaultConfig(ctx,
		config.WithRegion("us-east-1"),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(settings.AccessKey, settings.SecretKey, "")),
	)
	if err != nil {
		return nil, fmt.Errorf("configure S3 client: %w", err)
	}
	return &S3Store{
		client: s3.NewFromConfig(awsConfig, func(options *s3.Options) {
			options.BaseEndpoint = aws.String(settings.Endpoint)
			options.UsePathStyle = true
		}),
		bucket: settings.Bucket,
	}, nil
}

func (s *S3Store) Upload(ctx context.Context, functionID string, archive []byte) (UploadResult, error) {
	if strings.TrimSpace(functionID) == "" || len(archive) == 0 {
		return UploadResult{}, fmt.Errorf("function ID and archive are required")
	}
	digest := sha256.Sum256(archive)
	key := keyFor(functionID, hex.EncodeToString(digest[:]))
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(key),
		Body:          bytes.NewReader(archive),
		ContentLength: aws.Int64(int64(len(archive))),
		ContentType:   aws.String("application/gzip"),
	})
	if err != nil {
		return UploadResult{}, fmt.Errorf("%w: upload artifact %q: %w", ErrStorage, key, err)
	}
	return UploadResult{Key: key, SHA256: hex.EncodeToString(digest[:]), SizeBytes: int64(len(archive))}, nil
}

func (s *S3Store) Verify(ctx context.Context, functionID, key string) error {
	functionPrefix := keyPrefix(functionID)
	if !strings.HasPrefix(key, functionPrefix) || !strings.HasSuffix(key, ".tar.gz") {
		return ErrNotFound
	}
	digest := strings.TrimSuffix(strings.TrimPrefix(key, functionPrefix), ".tar.gz")
	if len(digest) != 64 {
		return ErrNotFound
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return ErrNotFound
	}
	_, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		var apiError smithy.APIError
		if errors.As(err, &apiError) && (apiError.ErrorCode() == "NotFound" || apiError.ErrorCode() == "NoSuchKey" || apiError.ErrorCode() == "404") {
			return ErrNotFound
		}
		return fmt.Errorf("%w: check artifact %q: %w", ErrStorage, key, err)
	}
	return nil
}

func (s *S3Store) Download(ctx context.Context, key string) ([]byte, error) {
	object, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		return nil, fmt.Errorf("%w: download artifact %q: %w", ErrStorage, key, err)
	}
	defer object.Body.Close()
	return io.ReadAll(object.Body)
}

func keyFor(functionID, digest string) string {
	return keyPrefix(functionID) + digest + ".tar.gz"
}

func keyPrefix(functionID string) string {
	functionDigest := sha256.Sum256([]byte(strings.TrimSpace(functionID)))
	return fmt.Sprintf("functions/%x/artifacts/", functionDigest)
}
