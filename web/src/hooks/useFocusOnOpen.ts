import { type RefObject, useEffect } from "react";

// Focuses the element when `open` turns on. A floating panel stays hidden until
// it is placed, and a hidden element refuses focus, so it retries for a few frames.
export default function useFocusOnOpen(open: boolean, ref: RefObject<HTMLElement | null>) {
    useEffect(() => {
        if (!open) return;
        let frame = 0;
        let tries = 0;
        const attempt = () => {
            const el = ref.current;
            if (el && document.activeElement !== el) el.focus({ preventScroll: true });
            if (document.activeElement !== ref.current && ++tries < 20) frame = requestAnimationFrame(attempt);
        };
        attempt();
        return () => cancelAnimationFrame(frame);
    }, [open, ref]);
}
