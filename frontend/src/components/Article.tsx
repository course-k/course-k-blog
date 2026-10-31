import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link } from "@tanstack/react-router";
import { dateLabel } from "../lib/types";
import type { PublicPost } from "../lib/types";
import { Markdown } from "./Markdown";

type Zoomed = { src: string; alt: string; trigger: HTMLElement };

export function Article({ post }: { post: PublicPost }) {
  const [zoomed, setZoomed] = useState<Zoomed | null>(null);
  const zoom = useCallback((image: HTMLImageElement, trigger: HTMLElement) => {
    setZoomed({ src: image.currentSrc || image.src, alt: image.alt, trigger });
  }, []);
  const close = useCallback(() => setZoomed(null), []);
  const images = useMemo(() => ({ mode: "public" as const, zoom }), [zoom]);
  return (
    <article className="reading">
      <Link to="/" search={{ page: 1 }} className="back">
        ← 記事一覧
      </Link>
      <header>
        <p className="post-meta">
          <time dateTime={post.publishedAt}>{dateLabel(post.publishedAt)}</time>
        </p>
        <h1>{post.title}</h1>
        {post.updatedAt !== post.publishedAt && (
          <p className="muted">更新：{dateLabel(post.updatedAt)}</p>
        )}
      </header>
      <Markdown text={post.markdown} toc prefix="article" images={images} />
      {zoomed && <ImageLightbox image={zoomed} onClose={close} />}
    </article>
  );
}

/** Enlarged view over the article: fits the screen, never beyond the original
 * size. Closes with the button, the backdrop or Escape, then restores the
 * scroll position and focus. */
function ImageLightbox({ image, onClose }: { image: Zoomed; onClose: () => void }) {
  const close = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    const scrollY = window.scrollY;
    const overflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    close.current?.focus();
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        onClose();
      } else if (event.key === "Tab") {
        // Only the close button is focusable inside the dialog.
        event.preventDefault();
        close.current?.focus();
      }
    };
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("keydown", onKey);
      document.body.style.overflow = overflow;
      window.scrollTo({ top: scrollY });
      image.trigger.focus({ preventScroll: true });
    };
  }, [image, onClose]);
  return (
    <div
      className="lightbox"
      role="dialog"
      aria-modal="true"
      aria-label={image.alt ? `拡大表示：${image.alt}` : "画像の拡大表示"}
      onClick={(event) => {
        if (event.target === event.currentTarget) onClose();
      }}
    >
      <button ref={close} type="button" className="lightbox-close" onClick={onClose}>
        閉じる
      </button>
      <img src={image.src} alt={image.alt} />
    </div>
  );
}
