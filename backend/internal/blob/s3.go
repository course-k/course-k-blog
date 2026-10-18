package blob

import (
	"bytes"
	"context"
	"errors"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

type S3 struct {
	Client *s3.Client
	Bucket string
}

func NewS3(ctx context.Context, bucket string) (*S3, error) {
	settings, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, errors.New("AWS storage configuration unavailable")
	}
	return &S3{Client: s3.NewFromConfig(settings), Bucket: bucket}, nil
}
func (s *S3) Put(ctx context.Context, key string, body []byte, mime string) error {
	if !validKey.MatchString(key) {
		return errors.New("invalid object key")
	}
	_, err := s.Client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(s.Bucket), Key: aws.String(key), Body: bytes.NewReader(body), ContentType: aws.String(mime), ServerSideEncryption: types.ServerSideEncryptionAes256, IfNoneMatch: aws.String("*")})
	if err != nil {
		return errors.New("private object write failed")
	}
	return nil
}
func (s *S3) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if !validKey.MatchString(key) {
		return nil, errors.New("invalid object key")
	}
	object, err := s.Client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.Bucket), Key: aws.String(key)})
	if err != nil {
		var apiError smithy.APIError
		if errors.As(err, &apiError) && apiError.ErrorCode() == "NoSuchKey" {
			return nil, ErrNotFound
		}
		return nil, errors.New("private object read failed")
	}
	return object.Body, nil
}
func (s *S3) Delete(ctx context.Context, key string) error {
	if !validKey.MatchString(key) {
		return errors.New("invalid object key")
	}
	_, err := s.Client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.Bucket), Key: aws.String(key)})
	if err != nil {
		return errors.New("private object removal failed")
	}
	return nil
}
