package publication

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

// Bucket keeps the public files in S3. The bucket itself stays private; the
// content delivery network in front of it is the only reader.
type Bucket struct {
	Client *s3.Client
	Name   string
}

func NewBucket(ctx context.Context, name string) (Bucket, error) {
	settings, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return Bucket{}, errors.New("AWS storage configuration unavailable")
	}
	return Bucket{Client: s3.NewFromConfig(settings), Name: name}, nil
}

func (b Bucket) Put(ctx context.Context, key string, body []byte, mime string) error {
	if !validKey.MatchString(key) {
		return errors.New("invalid public file key")
	}
	_, err := b.Client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(b.Name), Key: aws.String(key), Body: bytes.NewReader(body), ContentType: aws.String(mime), ServerSideEncryption: types.ServerSideEncryptionAes256})
	if err != nil {
		return errors.New("public file write failed")
	}
	return nil
}

// Get reports ErrNotFound only when storage says the object does not exist.
// Any other failure, such as a refused read, is a plain error.
func (b Bucket) Get(ctx context.Context, key string) ([]byte, error) {
	if !validKey.MatchString(key) {
		return nil, errors.New("invalid public file key")
	}
	object, err := b.Client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(b.Name), Key: aws.String(key)})
	if err != nil {
		var apiError smithy.APIError
		if errors.As(err, &apiError) && apiError.ErrorCode() == "NoSuchKey" {
			return nil, ErrNotFound
		}
		return nil, errors.New("public file read failed")
	}
	defer object.Body.Close()
	body, err := io.ReadAll(object.Body)
	if err != nil {
		return nil, errors.New("public file read failed")
	}
	return body, nil
}

func (b Bucket) Delete(ctx context.Context, key string) error {
	if !validKey.MatchString(key) {
		return errors.New("invalid public file key")
	}
	if _, err := b.Client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(b.Name), Key: aws.String(key)}); err != nil {
		return errors.New("public file removal failed")
	}
	return nil
}
