// AIBusyStatus is the one "AI is working" line every AI surface shows: the
// thinking mark, a shimmering label that steps through what it is doing, and
// a cancel (or, while text is arriving, a stop that keeps it). Hosts give it a
// pill (AI_PILL_CLASS) or a row of their own.

import React from "react";
import { AnimatePresence, motion } from "framer-motion";
import { SquareIcon, XIcon } from "lucide-react";
import AgentMark from "@/components/app/agent/AgentMark";

export const AI_PILL_CLASS =
    "h-8 pl-3 pr-1.5 rounded-full border border-slate-200 bg-white/95 backdrop-blur shadow-[0_8px_24px_-8px_rgba(15,23,42,0.25)] flex items-center gap-2";

export default function AIBusyStatus({
    labels,
    onCancel,
    onStop,
}: {
    // One label follows the real stage; several step on a timer.
    labels: string[];
    onCancel?: () => void;
    onStop?: () => void;
}) {
    const [stage, setStage] = React.useState(0);
    React.useEffect(() => {
        if (labels.length < 2) return;
        const t = setInterval(() => setStage((s) => Math.min(s + 1, labels.length - 1)), 1500);
        return () => clearInterval(t);
    }, [labels.length]);
    const label = labels[Math.min(stage, labels.length - 1)];

    return (
        <>
            <AgentMark variant="bare" size={14} state="thinking" />
            <AnimatePresence mode="wait" initial={false}>
                <motion.span
                    key={label}
                    initial={{ opacity: 0, y: 3 }}
                    animate={{ opacity: 1, y: 0 }}
                    exit={{ opacity: 0, y: -3 }}
                    transition={{ duration: 0.15 }}
                    className="ai-shimmer-text whitespace-nowrap text-[12px] font-medium"
                >
                    {label}
                </motion.span>
            </AnimatePresence>
            {onStop ? (
                <button
                    type="button"
                    onClick={onStop}
                    title="Stop and keep what it wrote (Esc)"
                    className="ml-auto h-6 shrink-0 rounded-full pl-1.5 pr-2 inline-flex items-center gap-1 text-[11px] font-medium text-slate-500 hover:text-slate-900 hover:bg-slate-100 transition-colors"
                >
                    <SquareIcon className="w-2.5 h-2.5 fill-current" />
                    Stop
                </button>
            ) : (
                onCancel && (
                    <button
                        type="button"
                        onClick={onCancel}
                        aria-label="Cancel"
                        title="Cancel (Esc)"
                        className="ml-auto size-6 shrink-0 rounded-full inline-flex items-center justify-center text-slate-400 hover:text-slate-900 hover:bg-slate-100 transition-colors"
                    >
                        <XIcon className="w-3.5 h-3.5" />
                    </button>
                )
            )}
        </>
    );
}
