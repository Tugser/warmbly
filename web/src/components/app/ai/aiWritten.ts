// aiWritten tints the passage AI is writing or has just written, and draws a
// blinking caret at its end while text is still arriving. The tint follows
// edits around it and leaves when the passage is kept or discarded.

import type { Editor } from "@tiptap/react";
import { Plugin, PluginKey } from "@tiptap/pm/state";
import { Decoration, DecorationSet } from "@tiptap/pm/view";

interface Mark {
    from: number;
    to: number;
    writing: boolean;
}

const key = new PluginKey<DecorationSet>("aiWritten");

function caret(): HTMLElement {
    const el = document.createElement("span");
    el.className = "ai-stream-caret";
    el.setAttribute("aria-hidden", "true");
    return el;
}

const plugin = () =>
    new Plugin<DecorationSet>({
        key,
        state: {
            init: () => DecorationSet.empty,
            apply(tr, set) {
                const mark = tr.getMeta(key) as Mark | null | undefined;
                if (mark === null) return DecorationSet.empty;
                if (!mark) return set.map(tr.mapping, tr.doc);
                const decos = mark.to > mark.from ? [Decoration.inline(mark.from, mark.to, { class: "ai-written" })] : [];
                if (mark.writing) decos.push(Decoration.widget(mark.to, caret, { side: 1, key: "ai-caret" }));
                return DecorationSet.create(tr.doc, decos);
            },
        },
        props: { decorations: (state) => key.getState(state) },
    });

// markAIWritten tints [from, to] (writing adds the caret); null clears it.
export function markAIWritten(editor: Editor, mark: Mark | null) {
    if (editor.isDestroyed) return;
    if (!key.getState(editor.state)) editor.registerPlugin(plugin());
    editor.view.dispatch(editor.state.tr.setMeta(key, mark).setMeta("addToHistory", false));
}
