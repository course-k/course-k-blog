import type { ArticlePage, PublicPost } from './src/lib/types'

type Options = { fetch?: typeof globalThis.fetch; env?: Record<string, string | undefined> }

export class NotFound extends Error {}
export function loadArticlePage(filter: { page: number; month?: string }, options?: Options): Promise<ArticlePage>
export function loadPost(id: string, options?: Options): Promise<PublicPost>
