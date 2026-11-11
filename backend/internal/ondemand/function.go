package ondemand

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/lambda/types"
)

// Function is the Lambda function this process runs in. It invokes itself to
// carry out pending work outside the request that caused it.
type Function struct {
	Client *lambda.Client
	Name   string
}

func NewFunction(ctx context.Context, name string) (Function, error) {
	settings, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return Function{}, errors.New("AWS function configuration unavailable")
	}
	return Function{Client: lambda.NewFromConfig(settings), Name: name}, nil
}

// Request queues one work invocation. It never fails the caller: the change
// that caused it is already saved, and pending work stays recorded in the
// database until a later invocation carries it out.
func (f Function) Request(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_, err := f.Client.Invoke(ctx, &lambda.InvokeInput{
		FunctionName:   aws.String(f.Name),
		InvocationType: types.InvocationTypeEvent,
		Payload:        []byte(`{"work":"pending"}`),
	})
	if err != nil {
		slog.Error("operation_failed", "operation", "on_demand_work", "code", "request_failed")
	}
}
