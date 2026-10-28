import Request from "@/lib/api/client/Request";
import getToken from "@/lib/helper/getToken";
import { API_URL } from "@/lib/information";
import { SESSION_ENDED_EVENT } from "@/lib/auth";

export function directDashboardImage(src: string): boolean {
    if (/^(data:image\/|blob:)/i.test(src)) return true;
    try {
        const url = new URL(src, window.location.href);
        if (url.username || url.password || !/^https?:$/.test(url.protocol)) return false;
        return url.origin === window.location.origin || url.origin === new URL(API_URL || window.location.origin).origin;
    } catch {
        return false;
    }
}

type ImageLease = { promise: Promise<string>; release: () => void };
type ImageEntry = { promise: Promise<string>; refs: number; controller: AbortController; url?: string };
const images = new Map<string, ImageEntry>();

if (typeof window !== "undefined") window.addEventListener(SESSION_ENDED_EVENT, () => {
    for (const image of images.values()) {
        image.controller.abort();
        if (image.url) URL.revokeObjectURL(image.url);
        image.url = undefined;
    }
    images.clear();
});

// Only mounted previews retain image bytes; saved email HTML never receives these URLs.
export function acquireDashboardImage(src: string): ImageLease {
    if (directDashboardImage(src)) return { promise: Promise.resolve(src), release: () => {} };
    const authorized = !!getToken();
    const key = `${authorized ? "session" : "public"}:${src}`;
    let entry = images.get(key);
    if (!entry) {
        const controller = new AbortController();
        const created: ImageEntry = { refs: 0, controller, promise: Promise.resolve("") };
        created.promise = Request<Blob>({
            method: "POST",
            url: authorized ? "/dashboard-image" : "/dashboard-image/public",
            authorization: authorized,
            responseType: "blob",
            data: { url: src },
            signal: controller.signal,
        }).then(async (blob) => {
            if (controller.signal.aborted) throw new DOMException("Image preview cancelled", "AbortError");
            if (!blob.type.startsWith("image/")) throw new Error("Not an image");
            const image = blob.type === "image/svg+xml" ? await rasterizeSvg(blob) : blob;
            if (controller.signal.aborted) throw new DOMException("Image preview cancelled", "AbortError");
            created.url = URL.createObjectURL(image);
            return created.url;
        });
        entry = created;
        images.set(key, entry);
    }
    entry.refs++;
    let released = false;
    return {
        promise: entry.promise,
        release: () => {
            if (released) return;
            released = true;
            if (--entry.refs === 0) {
                entry.controller.abort();
                if (entry.url) URL.revokeObjectURL(entry.url);
                if (images.get(key) === entry) images.delete(key);
            }
        },
    };
}

const SVG_RASTER_MAX = 512;

// Drawn to a PNG so no URL this origin hands out is an SVG document.
function rasterizeSvg(svg: Blob): Promise<Blob> {
    const source = URL.createObjectURL(svg);
    return new Promise<Blob>((resolve, reject) => {
        const img = new Image();
        img.onload = () => {
            const ratio = img.naturalWidth > 0 && img.naturalHeight > 0 ? img.naturalWidth / img.naturalHeight : 1;
            const width = ratio >= 1 ? SVG_RASTER_MAX : Math.max(1, Math.round(SVG_RASTER_MAX * ratio));
            const height = ratio >= 1 ? Math.max(1, Math.round(SVG_RASTER_MAX / ratio)) : SVG_RASTER_MAX;
            const canvas = document.createElement("canvas");
            canvas.width = width;
            canvas.height = height;
            const context = canvas.getContext("2d");
            if (!context) {
                reject(new Error("Not an image"));
                return;
            }
            context.drawImage(img, 0, 0, width, height);
            canvas.toBlob((png) => (png ? resolve(png) : reject(new Error("Not an image"))), "image/png");
        };
        img.onerror = () => reject(new Error("Not an image"));
        img.src = source;
    }).finally(() => URL.revokeObjectURL(source));
}
