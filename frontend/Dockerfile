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
USER node
EXPOSE 3000
CMD ["npm", "start"]
