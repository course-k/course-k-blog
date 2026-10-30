export const siteName = "course_kのブログ";
export const siteOrigin = "https://blog.course-k.com";
export const siteDescription = "技術のこと、日々のこと。このブログと、Qiita・note・Wantedlyに書いたものをまとめています。";

export function sharingMeta({
  title = siteName,
  description = siteDescription,
  image = `${siteOrigin}/brand/ogp-home.png`,
  url = `${siteOrigin}/`,
  type = "website",
}: {
  title?: string;
  description?: string;
  image?: string;
  url?: string;
  type?: "website" | "article";
} = {}) {
  return [
    { property: "og:site_name", content: siteName },
    { property: "og:locale", content: "ja_JP" },
    { property: "og:type", content: type },
    { property: "og:title", content: title },
    { property: "og:description", content: description },
    { property: "og:url", content: url },
    { property: "og:image", content: image },
    { property: "og:image:type", content: "image/png" },
    { property: "og:image:width", content: "1200" },
    { property: "og:image:height", content: "630" },
    { property: "og:image:alt", content: title },
    { name: "twitter:card", content: "summary_large_image" },
    { name: "twitter:title", content: title },
    { name: "twitter:description", content: description },
    { name: "twitter:image", content: image },
    { name: "twitter:image:alt", content: title },
  ];
}
