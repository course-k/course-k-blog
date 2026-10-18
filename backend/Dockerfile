FROM golang:1.27.2-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go test ./... && CGO_ENABLED=0 go build -trimpath -o /api ./cmd/api

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*
COPY --from=build /api /usr/local/bin/api
COPY certs/rds-global-bundle.crt /etc/ssl/certs/aws-rds-global-bundle.pem
ENV PGSSLROOTCERT=/etc/ssl/certs/aws-rds-global-bundle.pem
USER 65532:65532
EXPOSE 8081
ENTRYPOINT ["/usr/local/bin/api"]
