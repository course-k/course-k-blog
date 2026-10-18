FROM postgres:17-alpine
COPY infra/database/bootstrap.psql /bootstrap.psql
COPY backend/certs/rds-global-bundle.crt /rds-global-bundle.crt
ENV PGSSLROOTCERT=/rds-global-bundle.crt PGSSLMODE=verify-full
USER postgres
ENTRYPOINT ["psql", "-X", "-v", "ON_ERROR_STOP=1", "-f", "/bootstrap.psql"]
