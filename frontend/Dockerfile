FROM node:24-bookworm-slim AS build
WORKDIR /web
COPY package.json package-lock.json ./
RUN npm ci
COPY . .
RUN npm run build && npm run typecheck

FROM node:24-bookworm-slim
WORKDIR /web
ENV NODE_ENV=production PORT=3000 HOST=0.0.0.0
COPY --from=build /web/package.json /web/package-lock.json ./
RUN npm ci --omit=dev && npm cache clean --force
COPY --from=build /web/dist ./dist
# On Lambda this extension turns invocations into HTTP requests to the server
# below. Anywhere else nothing loads it and the image runs as before.
COPY --from=public.ecr.aws/awsguru/aws-lambda-adapter:1.1.0 /lambda-adapter /opt/extensions/lambda-adapter
COPY server.mjs runtime.mjs ogp.mjs publicData.mjs healthcheck.mjs ./
COPY assets ./assets
RUN mkdir -p /tmp/course-k-blog-fontconfig && chown node:node /tmp/course-k-blog-fontconfig
USER node
EXPOSE 3000
CMD ["npm", "start"]
