FROM golang:1.27.2-bookworm AS build
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=0 go build -trimpath -o /bootstrap ./cmd/bootstrap && CGO_ENABLED=0 go build -trimpath -o /restorecheck ./cmd/restorecheck
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*
COPY --from=build /bootstrap /usr/local/bin/bootstrap
COPY --from=build /restorecheck /usr/local/bin/restorecheck
COPY backend/certs/rds-global-bundle.crt /etc/ssl/certs/aws-rds-global-bundle.pem
ENV DATABASE_CA_FILE=/etc/ssl/certs/aws-rds-global-bundle.pem
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/bootstrap"]
