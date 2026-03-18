package services

import (
	"context"
	"fmt"
	"io"
	"path"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	appcfg "github.com/yourorg/whatsapp-s3-uploader/config"
)

type S3Service struct {
	client *s3.Client
	bucket string
}

func NewS3Service(cfg *appcfg.Config) (*S3Service, error) {
	awsCfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion(cfg.AWSRegion),
		// If you're running on EC2 with an IAM role, remove the credentials
		// provider below — the SDK will pick up the instance metadata automatically.
		config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(
				cfg.AWSAccessKeyID,
				cfg.AWSSecretKey,
				"",
			),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("loading aws config: %w", err)
	}

	return &S3Service{
		client: s3.NewFromConfig(awsCfg),
		bucket: cfg.S3Bucket,
	}, nil
}

// UploadResult holds the S3 object key and public-friendly URL after a successful upload.
type UploadResult struct {
	Key string
	URL string
}

// Upload streams the given reader to S3 under a time-prefixed key.
// filename is used to build the object key (e.g. "documents/invoice.pdf").
// contentType is the MIME type (e.g. "application/pdf").
// size is the content length in bytes (-1 if unknown — S3 needs chunked upload then).
func (s *S3Service) Upload(ctx context.Context, r io.Reader, folder, filename, contentType string, size int64) (*UploadResult, error) {
	key := buildKey(folder, filename)

	input := &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		Body:        r,
		ContentType: aws.String(contentType),
	}
	if size > 0 {
		input.ContentLength = aws.Int64(size)
	}

	if _, err := s.client.PutObject(ctx, input); err != nil {
		return nil, fmt.Errorf("s3 put object: %w", err)
	}

	// Generate a presigned URL valid for 24 hours.
	presignClient := s3.NewPresignClient(s.client)
	presigned, err := presignClient.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(24*time.Hour))
	if err != nil {
		return nil, fmt.Errorf("presign url: %w", err)
	}

	return &UploadResult{
		Key: key,
		URL: presigned.URL,
	}, nil
}

// buildKey constructs a unique S3 object key.
// Example: "whatsapp/images/20240617-153042-invoice.jpg"
func buildKey(folder, filename string) string {
	timestamp := time.Now().UTC().Format("20060102-150405")
	ext := path.Ext(filename)
	base := filename[:len(filename)-len(ext)]
	return fmt.Sprintf("whatsapp/%s/%s-%s%s", folder, timestamp, base, ext)
}
