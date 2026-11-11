FROM golang:1.27.4-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go test ./... && CGO_ENABLED=0 go build -trimpath -o /api ./cmd/api

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*
COPY --from=build /api /usr/local/bin/api
# On Lambda this extension turns invocations into HTTP requests to the server
# below. Anywhere else nothing loads it and the image runs as before.
COPY --from=public.ecr.aws/awsguru/aws-lambda-adapter:1.1.0 /lambda-adapter /opt/extensions/lambda-adapter
COPY certs/rds-global-bundle.crt /etc/ssl/certs/aws-rds-global-bundle.pem
ENV DATABASE_CA_FILE=/etc/ssl/certs/aws-rds-global-bundle.pem
RUN mkdir -p /data/media /backup/archives /public && chown -R 65532:65532 /data /backup /public
USER 65532:65532
EXPOSE 8081
ENTRYPOINT ["/usr/local/bin/api"]
