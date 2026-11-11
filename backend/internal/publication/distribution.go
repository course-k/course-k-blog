package publication

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront/types"
)

// Distribution is the content delivery network in front of the public files
// and the rendered pages.
type Distribution struct {
	Client *cloudfront.Client
	ID     string
}

func NewDistribution(ctx context.Context, id string) (Distribution, error) {
	settings, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return Distribution{}, errors.New("AWS delivery configuration unavailable")
	}
	return Distribution{Client: cloudfront.NewFromConfig(settings), ID: id}, nil
}

// Purge discards every cached response. Pages, article images and reader files
// all change with publication, and one wildcard counts as a single path.
func (d Distribution) Purge(ctx context.Context) error {
	reference := make([]byte, 16)
	if _, err := rand.Read(reference); err != nil {
		return errors.New("cache purge reference unavailable")
	}
	_, err := d.Client.CreateInvalidation(ctx, &cloudfront.CreateInvalidationInput{
		DistributionId: aws.String(d.ID),
		InvalidationBatch: &types.InvalidationBatch{
			CallerReference: aws.String(hex.EncodeToString(reference)),
			Paths:           &types.Paths{Quantity: aws.Int32(1), Items: []string{"/*"}},
		},
	})
	if err != nil {
		return errors.New("cache purge failed")
	}
	return nil
}
