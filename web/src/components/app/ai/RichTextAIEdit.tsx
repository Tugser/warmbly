// RichTextAIEdit — "edit selection with AI" for TipTap surfaces (campaign step
// editor). Same floating pill + AIEditPopover as the textarea host; the editor
// gives us real selection coordinates via coordsAtPos. The rewrite replaces
// the selected range and stays selected for review; Undo restores a pre-edit
// HTML snapshot, which reverts the whole rewrite in one step rather than
// unwinding it through the editor's own history.
//
// The passage leaves and re-enters the document through richTextPassage, so
// merge variables and the other chips survive the trip and the replacement
// lands with paste semantics instead of splitting the paragraph it sits in.

import React from "react";
import { createPortal } from "react-dom";
import { AnimatePresence, motion } from "framer-motion";
import { SparklesIcon } from "lucide-react";
import type { Editor } from "@tiptap/react";
import AIEditPopover from "./AIEditPopover";
import AIRunCard from "./AIRunCard";
import { AI_CARD_CLASS, EDIT_REFINEMENTS, aiRunCardClass } from "./aiCard";
import useEditorSheen from "./useEditorSheen";
import useEditorAIRun from "./useEditorAIRun";
import useClickOutside from "@/hooks/useClickOutside";
import { clampCardLeft, AI_CARD_WIDTH } from "./floatingBounds";
import { clampContext, passageAIConfigs, passageText } from "./richTextPassage";

interface EditorRange {
    from: number;
    to: number;
    text: string;
    // Config of every AI block inside the range, so a token the model echoes
    // comes back as the block it was rather than an empty one.
    aiConfigs: Map<string, string>;
}

interface Anchor {
    top: number;
    bottom: number;
    centerX: number;
}

function anchorFor(editor: Editor, from: number, to: number): Anchor | null {
    try {
        const a = editor.view.coordsAtPos(from);
        const b = editor.view.coordsAtPos(to);
        const sameLine = Math.abs(a.top - b.top) < 4;
        return {
            top: a.top,
            bottom: b.bottom,
            centerX: sameLine ? (a.left + b.right) / 2 : (a.left + a.right) / 2 + 60,
        };
    } catch {
        return null;
    }
}

export default function RichTextAIEdit({ editor }: { editor: Editor }) {
    const [range, setRange] = React.useState<EditorRange | null>(null);
    const [anchor, setAnchor] = React.useState<Anchor | null>(null);
    const [open, setOpen] = React.useState(false);

    const rootRef = React.useRef<HTMLDivElement>(null);
    const frozen = React.useRef<EditorRange | null>(null);
    const openRef = React.useRef(open);
    openRef.current = open;

    const closeAll = React.useCallback(() => {
        setOpen(false);
        setRange(null);
        setAnchor(null);
        frozen.current = null;
    }, []);

    const ai = useEditorAIRun(editor, closeAll);
    const aiRef = React.useRef(ai);
    aiRef.current = ai;
    const busy = ai.phase === "thinking" || ai.phase === "writing" || ai.phase === "checking";
    useEditorSheen(editor.view.dom, ai.phase === "thinking");

    // Track selection while the popover is closed.
    React.useEffect(() => {
        const onSelection = () => {
            if (openRef.current) return;
            const { from, to, empty } = editor.state.selection;
            if (empty) {
                setRange(null);
                setAnchor(null);
                return;
            }
            setRange({
                from,
                to,
                text: passageText(editor, from, to),
                aiConfigs: passageAIConfigs(editor, from, to),
            });
            setAnchor(anchorFor(editor, from, to));
        };
        editor.on("selectionUpdate", onSelection);
        return () => {
            editor.off("selectionUpdate", onSelection);
        };
    }, [editor]);

    // Follow scrolling/resizes.
    React.useEffect(() => {
        if (!range && !frozen.current) return;
        const sync = () => {
            const target = aiRef.current.range() ?? frozen.current ?? range;
            if (!target) return;
            setAnchor(anchorFor(editor, target.from, target.to));
        };
        window.addEventListener("scroll", sync, true);
        window.addEventListener("resize", sync);
        return () => {
            window.removeEventListener("scroll", sync, true);
            window.removeEventListener("resize", sync);
        };
    }, [range, editor]);

    // The passage grows while it is written; the card stays with it.
    React.useEffect(() => {
        if (!busy && ai.phase !== "review") return;
        let frame = 0;
        const follow = () => {
            const r = aiRef.current.range();
            if (r) setAnchor(anchorFor(editor, r.from, r.to));
            if (busy) frame = requestAnimationFrame(follow);
        };
        follow();
        return () => cancelAnimationFrame(frame);
    }, [busy, ai.phase, editor]);

    // Escape steps back: stop the writing, then discard it, then close.
    // Clicking away keeps a finished rewrite and leaves a running one running.
    const editorDom = React.useMemo(() => ({ get current() { return editor.view.dom; } }), [editor]);
    useClickOutside(
        open,
        (cause) => {
            const run = aiRef.current;
            if (cause === "escape") {
                if (run.phase === "writing") run.stop();
                else if (run.phase !== "idle") run.discard();
                else {
                    closeAll();
                    editor.commands.focus();
                }
                return false;
            }
            if (run.phase === "review") run.keep();
            else if (run.phase === "idle") closeAll();
            return false;
        },
        [editorDom, rootRef],
    );

    const run = (instruction: string) => {
        const t = frozen.current;
        if (!t || ai.phase !== "idle") return;
        ai.run({
            from: t.from,
            to: t.to,
            select: "range",
            original: t.text,
            aiConfigs: t.aiConfigs,
            request: {
                path: "/generation/edit",
                body: { text: t.text, instruction, context: clampContext(editor.getText()) },
            },
        });
    };

    if (typeof document === "undefined") return null;

    const showPill = !open && !!range && !!anchor && range.text.trim().length > 1;
    const popAbove = (anchor?.top ?? 0) > 200;
    const popLeft = clampCardLeft(anchor?.centerX ?? 0, AI_CARD_WIDTH, editor.view.dom);

    return createPortal(
        <div ref={rootRef} data-floating="">
            <AnimatePresence>
                {showPill && anchor && (
                    <motion.button
                        key="ai-pill"
                        type="button"
                        initial={{ opacity: 0, y: 4, scale: 0.92, x: "-50%" }}
                        animate={{ opacity: 1, y: 0, scale: 1, x: "-50%" }}
                        exit={{ opacity: 0, y: 2, scale: 0.95, x: "-50%" }}
                        transition={{ type: "spring", stiffness: 500, damping: 32 }}
                        style={{ position: "fixed", top: anchor.top - 34, left: anchor.centerX, zIndex: 150 }}
                        className="h-7 pl-2 pr-2.5 rounded-full border border-slate-200 bg-white shadow-[0_6px_20px_-6px_rgba(15,23,42,0.25)] inline-flex items-center gap-1.5 text-[11.5px] font-medium text-slate-700 hover:text-sky-700 hover:border-sky-300 transition-colors"
                        onMouseDown={(e) => {
                            e.preventDefault();
                            if (!range || ai.phase !== "idle") return;
                            frozen.current = range;
                            setOpen(true);
                        }}
                    >
                        <SparklesIcon className="w-3 h-3 text-sky-500" />
                        Edit with AI
                    </motion.button>
                )}
                {open && anchor && (
                    <motion.div
                        key="ai-popover"
                        initial={{ opacity: 0, scale: 0.96, y: popAbove ? 4 : -4 }}
                        animate={{ opacity: 1, scale: 1, y: 0 }}
                        exit={{ opacity: 0, scale: 0.97, y: popAbove ? 2 : -2 }}
                        transition={{ duration: 0.14, ease: [0.16, 1, 0.3, 1] }}
                        style={{
                            position: "fixed",
                            left: popLeft,
                            zIndex: 150,
                            ...(popAbove
                                ? { bottom: window.innerHeight - anchor.top + 8 }
                                : { top: anchor.bottom + 8 }),
                        }}
                        className={ai.phase === "idle" ? AI_CARD_CLASS : aiRunCardClass(ai.phase)}
                    >
                        {ai.phase === "idle" ? (
                            <AIEditPopover phase="idle" usage={null} onRun={run} onUndo={closeAll} onRetry={closeAll} onDone={closeAll} />
                        ) : (
                            <AIRunCard run={ai} thinking="Reading the passage…" writing="Rewriting…" refinements={EDIT_REFINEMENTS} />
                        )}
                    </motion.div>
                )}
            </AnimatePresence>
        </div>,
        document.body,
    );
}
