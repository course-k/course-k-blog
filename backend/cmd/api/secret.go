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
	result, err := p.Client.GetParameter(ctx, &ssm.GetParameterInput{Name: aws.String(name), WithDecryption: aws.Bool(true)})
	if err != nil || result.Parameter == nil || result.Parameter.Value == nil {
		return "", errors.New("parameter unavailable")
	}
	return *result.Parameter.Value, nil
}

// fetchParameter reads one decrypted parameter with the process's AWS identity.
func fetchParameter(ctx context.Context, name string) (string, error) {
	settings, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return "", errors.New("AWS parameter configuration unavailable")
	}
	return parameterStore{Client: ssm.NewFromConfig(settings)}.fetch(ctx, name)
}
