export type PublicPost = {
  id: string; title: string; markdown: string; publishedAt: string; updatedAt: string
}
export type Draft = {
  id: string; title: string; markdown: string; version: number; published: boolean;
  publishedAt: string | null; updatedAt: string; hasUnpublishedChanges: boolean
}
export function dateLabel(value: string) {
  return new Intl.DateTimeFormat('ja-JP', { timeZone: 'Asia/Tokyo', year: 'numeric', month: 'long', day: 'numeric' }).format(new Date(value))
}
