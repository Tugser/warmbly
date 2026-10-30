// useWritingStream runs a streamed generation and reveals its text at a steady
// pace: a burst of tokens, or a backend that answers all at once, still reads
// as writing. The reveal never ends inside a half-written merge field, so a
// token appears as a chip rather than as `{{.Fir`.

import React from "react";
import { useQueryClient } from "@tanstack/react-query";
import streamWriting from "@/lib/api/client/app/generation/streamWriting";
import type { EditRequest, WriteRequest, WriteResponse } from "@/lib/api/models/app/generation/Write";
import { prefersReducedMotion } from "./useTypewriter";

export type WritingRequest = { path: "/generation/write"; body: WriteRequest } | { path: "/generation/edit"; body: EditRequest };

export interface WritingStreamCallbacks {
    // The text revealed so far; always a prefix of what the model wrote.
    onText: (shown: string) => void;
    onChecking?: () => void;
}

export function isAbort(e: unknown): boolean {
    return (e as Error)?.name === "AbortError";
}

// Cuts an unfinished `{{ ... }}` or `[[ai: ... ]]` off the end of a partial,
// and a lone brace that may be the start of one.
export function settledPrefix(s: string): string {
    for (const [open, close] of [["{{", "}}"], ["[[", "]]"]]) {
        const at = s.lastIndexOf(open);
        if (at >= 0 && s.indexOf(close, at) < 0) s = s.slice(0, at);
    }
    return s.replace(/[{[]$/, "");
}

export default function useWritingStream() {
    const qc = useQueryClient();
    const abort = React.useRef<AbortController | null>(null);
    const frame = React.useRef<number | null>(null);

    const halt = React.useCallback(() => {
        if (frame.current !== null) cancelAnimationFrame(frame.current);
        frame.current = null;
    }, []);

    // Stops the request; the run rejects with an AbortError.
    const stop = React.useCallback(() => {
        abort.current?.abort();
        abort.current = null;
        halt();
    }, [halt]);

    const run = React.useCallback(
        async (req: WritingRequest, cb: WritingStreamCallbacks): Promise<WriteResponse> => {
            stop();
            const ac = new AbortController();
            abort.current = ac;
            const instant = prefersReducedMotion();
            let written = "";
            let shown = 0;
            let caughtUp: (() => void) | null = null;
            let broke: ((e: unknown) => void) | null = null;
            let failure: unknown = null;

            // Each frame closes a share of the gap, so the reveal speeds up when
            // it falls behind and settles to a typing pace when it does not.
            const tick = () => {
                try {
                    if (shown < written.length) {
                        shown = instant ? written.length : Math.min(written.length, shown + Math.max(1, Math.ceil((written.length - shown) / 12)));
                        cb.onText(shown < written.length ? settledPrefix(written.slice(0, shown)) : written);
                    } else if (caughtUp) {
                        caughtUp();
                        return;
                    }
                } catch (e) {
                    failure = e;
                    broke?.(e);
                    return;
                }
                frame.current = requestAnimationFrame(tick);
            };
            frame.current = requestAnimationFrame(tick);

            try {
                const res = await streamWriting(
                    req.path,
                    req.body,
                    { onDelta: (d) => (written += d), onChecking: cb.onChecking },
                    ac.signal,
                );
                written = res.text;
                shown = Math.min(shown, written.length);
                if (failure) throw failure;
                await new Promise<void>((resolve, reject) => {
                    caughtUp = resolve;
                    broke = reject;
                    ac.signal.addEventListener("abort", () => reject(new DOMException("Stopped", "AbortError")), { once: true });
                });
                return res;
            } finally {
                halt();
                if (abort.current === ac) abort.current = null;
                void qc.invalidateQueries({ queryKey: ["subscription", "credits"] });
            }
        },
        [halt, qc, stop],
    );

    React.useEffect(() => stop, [stop]);

    return { run, stop };
}
