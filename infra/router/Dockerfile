FROM nginx:stable-alpine@sha256:970364228b1e4f932abc22d321932816782cca8a20b85a77552a7cfe9acfb3b5
COPY nginx.conf /etc/nginx/nginx.conf
RUN mkdir -p /tmp/nginx && chown nginx:nginx /tmp/nginx
# Fargate copies this directory ownership into the matching task scratch volume.
VOLUME ["/tmp/nginx"]
USER nginx
EXPOSE 8080
# Skip the image's configuration-rewriting entrypoint; the root filesystem is read-only.
ENTRYPOINT ["nginx", "-g", "daemon off;"]
