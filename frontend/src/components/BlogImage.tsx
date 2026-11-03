import {
  createContext,
  useContext,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type CSSProperties,
  type PointerEvent as ReactPointerEvent,
} from "react";
import type { Element } from "hast";
import {
  PENDING_PREFIX,
  parseDisplay,
  type ImageAlign,
  type ImageDisplay,
} from "../features/editor/imageMarkdown";

export type PendingImageView = {
  status: "queued" | "uploading" | "done" | "failed";
  error?: string;
  previewUrl?: string;
};

export type ImageEditing = {
  selected: number | null;
  select(start: number | null): void;
  change(start: number, display: Omit<ImageDisplay, "title">): void;
  pending(clientImageId: string): PendingImageView | undefined;
  retry(clientImageId: string): void;
  remove(clientImageId: string): void;
};

export type ImageContextValue = {
  mode: "public" | "preview";
  /** Public pages: open the enlarged view for the clicked image. */
  zoom?: (image: HTMLImageElement, trigger: HTMLElement) => void;
  editing?: ImageEditing;
};

export const ImageContext = createContext<ImageContextValue>({ mode: "public" });

const alignLabels: { align: ImageAlign; label: string }[] = [
  { align: "left", label: "左寄せ" },
  { align: "center", label: "中央寄せ" },
  { align: "right", label: "右寄せ" },
];

/** Shared by the public article and the editor preview so both lay out
 * width and alignment identically, including in SSR without JavaScript. */
export function BlogImage({
  node,
  src,
  alt,
  title,
}: {
  node?: Element;
  src?: string;
  alt?: string;
  title?: string;
}) {
  const context = useContext(ImageContext);
  const { display } = parseDisplay(title);
  const start = Number(node?.properties?.dataSourceStart ?? NaN);
  const inLink = Boolean(node?.properties?.dataInLink);
  const style = display.width
    ? ({ "--image-width": `${display.width}%` } as CSSProperties)
    : undefined;
  const frameRef = useRef<HTMLSpanElement>(null);
  const imageRef = useRef<HTMLImageElement>(null);
  const [loaded, setLoaded] = useState(false);
  useEffect(() => {
    if (imageRef.current?.complete && imageRef.current.naturalWidth) setLoaded(true);
  }, [src]);

  if (src?.startsWith(PENDING_PREFIX)) {
    const id = src.slice(PENDING_PREFIX.length);
    const pending = context.editing?.pending(id);
    if (!context.editing) return null;
    return (
      <span className="blog-image" data-align="left">
        <span className="blog-image-frame pending-image" data-status={pending?.status ?? "missing"}>
          {pending?.previewUrl && <img src={pending.previewUrl} alt={alt ?? ""} />}
          {pending?.status === "failed" ? (
            <span className="pending-image-actions" role="alert">
              <span>{pending.error ?? "画像の送信に失敗しました。"}</span>
              <button type="button" className="secondary" onClick={() => context.editing!.retry(id)}>
                再試行
              </button>
              <button type="button" className="quiet" onClick={() => context.editing!.remove(id)}>
                削除
              </button>
            </span>
          ) : pending ? (
            <span className="pending-image-label">画像を送信中…</span>
          ) : (
            <span className="pending-image-label">この画像は送信されていません。</span>
          )}
        </span>
      </span>
    );
  }

  const editing = context.mode === "preview" ? context.editing : undefined;
  const selected = editing && Number.isFinite(start) && editing.selected === start;
  const image = (
    <img
      ref={imageRef}
      src={src}
      alt={alt ?? ""}
      title={display.title}
      onLoad={() => setLoaded(true)}
      onClick={
        editing && Number.isFinite(start)
          ? (event) => {
              // In the preview a click edits the image instead of opening it.
              event.preventDefault();
              event.stopPropagation();
              editing.select(start);
            }
          : undefined
      }
    />
  );
  return (
    <span className="blog-image" data-align={display.align} style={style}>
      <span ref={frameRef} className="blog-image-frame">
        {context.mode === "public" && !inLink ? (
          <button
            type="button"
            className="image-zoom"
            aria-label={alt ? `画像を拡大表示：${alt}` : "画像を拡大表示"}
            onClick={(event) => {
              if (imageRef.current) context.zoom?.(imageRef.current, event.currentTarget);
            }}
          >
            {image}
          </button>
        ) : (
          image
        )}
        {selected && loaded && (
          <ImageControls
            start={start}
            display={display}
            frameRef={frameRef}
            imageRef={imageRef}
            editing={editing!}
          />
        )}
      </span>
    </span>
  );
}

function ImageControls({
  start,
  display,
  frameRef,
  imageRef,
  editing,
}: {
  start: number;
  display: ImageDisplay;
  frameRef: React.RefObject<HTMLSpanElement | null>;
  imageRef: React.RefObject<HTMLImageElement | null>;
  editing: ImageEditing;
}) {
  const drag = useRef<{
    pointerId: number;
    startX: number;
    startWidth: number;
    min: number;
    max: number;
    content: number;
    width: number;
  } | null>(null);
  const [label, setLabel] = useState<string | null>(null);

  const outer = () => frameRef.current?.parentElement as HTMLElement | null;
  const reset = () => {
    const element = outer();
    if (element) {
      element.style.removeProperty("width");
      element.style.removeProperty("margin-left");
      element.style.removeProperty("margin-right");
      element.removeAttribute("data-dragging");
    }
    drag.current = null;
    setLabel(null);
  };

  function onPointerDown(event: ReactPointerEvent<HTMLSpanElement>) {
    const image = imageRef.current;
    const element = outer();
    const prose = element?.closest(".prose") as HTMLElement | null;
    if (!image || !element || !prose || event.button !== 0) return;
    event.preventDefault();
    event.stopPropagation();
    const proseRect = prose.getBoundingClientRect();
    const imageRect = image.getBoundingClientRect();
    const content = proseRect.width;
    const left = imageRect.left - proseRect.left;
    const natural = image.naturalWidth || imageRect.width;
    // The left edge stays put while dragging and the right-edge handle follows
    // the pointer, even past the content edge; the alignment is re-applied on
    // release. The limit is the original size and the content width only, so a
    // right-aligned or centred image can grow back after being shrunk.
    const max = Math.max(1, Math.min(natural, content));
    const min = Math.min(natural, content * 0.1);
    drag.current = {
      pointerId: event.pointerId,
      startX: event.clientX,
      startWidth: imageRect.width,
      min,
      max,
      content,
      width: imageRect.width,
    };
    element.setAttribute("data-dragging", "true");
    element.style.width = `${imageRect.width}px`;
    element.style.marginLeft = `${left}px`;
    element.style.marginRight = "auto";
    event.currentTarget.setPointerCapture(event.pointerId);
    setLabel(`本文幅の${Math.round((imageRect.width / content) * 100)}%`);
  }
  function onPointerMove(event: ReactPointerEvent<HTMLSpanElement>) {
    const state = drag.current;
    const element = outer();
    if (!state || state.pointerId !== event.pointerId || !element) return;
    const width = Math.min(state.max, Math.max(state.min, state.startWidth + event.clientX - state.startX));
    state.width = width;
    element.style.width = `${width}px`;
    setLabel(`本文幅の${Math.max(10, Math.min(100, Math.round((width / state.content) * 100)))}%`);
  }
  function onPointerUp(event: ReactPointerEvent<HTMLSpanElement>) {
    const state = drag.current;
    if (!state || state.pointerId !== event.pointerId) return;
    const percent = Math.max(10, Math.min(100, Math.round((state.width / state.content) * 100)));
    reset();
    if (Math.abs(state.width - state.startWidth) >= 1)
      editing.change(start, { width: percent, align: display.align });
  }

  // Controls follow the rendered image box, which may be narrower than the
  // reserved width (small originals) and moves with alignment.
  const [box, setBox] = useState<{ left: number; top: number; width: number; height: number } | null>(null);
  useLayoutEffect(() => {
    const frame = frameRef.current;
    const image = imageRef.current;
    if (!frame || !image) return;
    const update = () => {
      const outer = frame.getBoundingClientRect();
      const rect = image.getBoundingClientRect();
      setBox({ left: rect.left - outer.left, top: rect.top - outer.top, width: rect.width, height: rect.height });
    };
    update();
    const observer = new ResizeObserver(update);
    observer.observe(frame);
    observer.observe(image);
    return () => observer.disconnect();
  }, [frameRef, imageRef, display.align, display.width]);
  if (!box) return null;
  return (
    <span className="image-selection" style={box}>
      <span className="image-toolbar" role="toolbar" aria-label="画像の配置とサイズ">
        {alignLabels.map(({ align, label: text }) => (
          <button
            key={align}
            type="button"
            aria-pressed={display.align === align}
            onClick={(event) => {
              event.stopPropagation();
              editing.change(start, { width: display.width, align });
            }}
          >
            {text}
          </button>
        ))}
        <button
          type="button"
          onClick={(event) => {
            event.stopPropagation();
            editing.change(start, { width: null, align: display.align });
          }}
        >
          サイズを戻す
        </button>
      </span>
      <span
        className="image-resize-handle"
        aria-hidden="true"
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMove}
        onPointerUp={onPointerUp}
        onPointerCancel={reset}
        onClick={(event) => event.stopPropagation()}
      />
      {label && (
        <span className="image-size-label" role="status">
          {label}
        </span>
      )}
    </span>
  );
}
