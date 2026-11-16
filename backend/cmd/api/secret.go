package main

import (
	"context"
	"errors"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

// databaseURL returns the connection URL given directly, or reads it from the
// named Parameter Store entry. Where the platform cannot inject secrets into
// the environment, the process fetches the one it needs itself. An empty
// result means neither is configured.
func databaseURL(ctx context.Context, getenv func(string) string, fetch func(context.Context, string) (string, error)) (string, error) {
	if direct := getenv("DATABASE_URL"); direct != "" {
		return direct, nil
	}
	name := getenv("DATABASE_URL_PARAMETER")
	if name == "" {
		return "", nil
	}
	value, err := fetch(ctx, name)
	if err != nil {
		return "", errors.New("cannot read the database URL parameter")
	}
	if value = strings.TrimSpace(value); value == "" {
		return "", errors.New("the database URL parameter is empty")
	}
	return value, nil
}

type parameterStore struct{ Client *ssm.Client }

func (p parameterStore) fetch(ctx context.Context, name string) (string, error) {
	// GetParameters is the action the runtime roles are already limited to,
	// the same one ECS uses to inject this value.
	result, err := p.Client.GetParameters(ctx, &ssm.GetParametersInput{Names: []string{name}, WithDecryption: aws.Bool(true)})
	if err != nil || len(result.Parameters) != 1 || result.Parameters[0].Value == nil {
		return "", errors.New("parameter unavailable")
	}
	return *result.Parameters[0].Value, nil
}

// fetchParameter reads one decrypted parameter with the process's AWS identity.
func fetchParameter(ctx context.Context, name string) (string, error) {
	settings, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return "", errors.New("AWS parameter configuration unavailable")
	}
	return parameterStore{Client: ssm.NewFromConfig(settings)}.fetch(ctx, name)
}
