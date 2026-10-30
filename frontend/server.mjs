import { serve } from 'srvx/node'
import { staticMiddleware } from 'srvx/static'
import { fileURLToPath } from 'node:url'
import entry from './dist/server/index.js'
import { createHandler } from './runtime.mjs'
import { createArticleImageHandler } from './ogp.mjs'

serve({
  hostname: process.env.HOST || '0.0.0.0',
  port: Number(process.env.PORT || 3000),
  trustProxy: false,
  gracefulShutdown: true,
  fetch: createHandler({
    articleImages: createArticleImageHandler(),
    render: request => entry.fetch(request),
    staticFiles: staticMiddleware({ dir: fileURLToPath(new URL('./dist/client/', import.meta.url)) }),
  }),
})
