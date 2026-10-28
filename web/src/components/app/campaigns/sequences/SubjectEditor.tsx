// SubjectEditor: the step subject as a one-line editor built from the body's
// pieces. Merge fields and conditions are chips, `{{` opens the same type-ahead
// (fields, sender, fallbacks and helpers), and ⌘J asks AI. The value stays
// plain text: every chip serializes back to its literal token.

import React from "react";
import { useEditor, EditorContent, type Editor } from "@tiptap/react";
import { Extension } from "@tiptap/core";
import Document from "@tiptap/extension-document";
import Paragraph from "@tiptap/extension-paragraph";
import Text from "@tiptap/extension-text";
import { UndoRedo } from "@tiptap/extensions";
import { GitBranchIcon, ShuffleIcon } from "lucide-react";
import { parseToken } from "@/lib/templateVars";
import { escapeHTML, oneLine, subjectInline, subjectToHTML } from "./subjectText";
import { VariableNode } from "./nodes/VariableNode";
import { ConditionalNode } from "./nodes/ConditionalNode";
import EditorSuggest from "./nodes/EditorSuggest";
import { VariableMenu } from "./RichTextEditor";
import { renderPreview } from "./emailPreview";

const OneLineDocument = Document.extend({ content: "paragraph" });

// Enter never splits the line, and ⌘J reaches the caller through a ref so the
// extension, created once, always calls the current handler.
function subjectKeys(onAI: React.RefObject<(() => void) | undefined>) {
    return Extension.create({
        name: "subjectKeys",
        addKeyboardShortcuts() {
            return {
                Enter: () => true,
                "Shift-Enter": () => true,
                "Mod-j": () => {
                    if (!onAI.current) return false;
                    onAI.current();
                    return true;
                },
            };
        },
    });
}

export interface SubjectEditorHandle {
    insert: (token: string) => void;
    focus: () => void;
}

export default React.forwardRef<
    SubjectEditorHandle,
    {
        value: string;
        onChange: (value: string) => void;
        placeholder?: string;
        variables: string[];
        // ⌘J and the AI button; omitted when the member cannot use AI.
        onAI?: () => void;
        // The AI trigger, rendered at the right edge of the field.
        aiSlot?: React.ReactNode;
    }
>(function SubjectEditor({ value, onChange, placeholder, variables, onAI, aiSlot }, ref) {
    const onAIRef = React.useRef(onAI);
    onAIRef.current = onAI;
    const onChangeRef = React.useRef(onChange);
    onChangeRef.current = onChange;

    const editorRef = React.useRef<Editor | null>(null);
    const editor = useEditor({
        injectCSS: false,
        extensions: [OneLineDocument, Paragraph, Text, UndoRedo, VariableNode, ConditionalNode, subjectKeys(onAIRef)],
        content: subjectToHTML(value),
        editorProps: {
            attributes: {
                class: "tiptap-body tiptap-subject min-h-[22px] text-[13.5px] leading-[22px] text-slate-800 focus:outline-none",
                "aria-label": "Subject",
                role: "textbox",
                "aria-multiline": "false",
            },
            // Pasted text joins onto the one line, and its tokens become chips.
            handlePaste: (view, event) => {
                const text = event.clipboardData?.getData("text/plain");
                if (!text) return false;
                event.preventDefault();
                editorRef.current?.chain().focus().insertContent(subjectInline(oneLine(text))).run();
                return true;
            },
            handleDrop: () => true,
        },
        onUpdate: ({ editor }) => onChangeRef.current(oneLine(editor.getText())),
    });
    editorRef.current = editor;

    // A value set from outside (AI, a template, switching steps) replaces the
    // line; the user's own typing already matches and is left alone.
    React.useEffect(() => {
        if (!editor || oneLine(editor.getText()) === value) return;
        editor.commands.setContent(subjectToHTML(value), { emitUpdate: false });
    }, [value, editor]);

    const insert = React.useCallback(
        (token: string) => {
            if (!editor) return;
            if (parseToken(token)) editor.chain().focus().insertVariable(token).run();
            else editor.chain().focus().insertContent(escapeHTML(token)).run();
        },
        [editor],
    );
    React.useImperativeHandle(ref, () => ({ insert, focus: () => editor?.commands.focus("end") }), [insert, editor]);

    if (!editor) return null;
    const rendered = renderPreview(value).trim();

    return (
        <div className="rounded-md border border-slate-200 bg-white transition-colors focus-within:border-sky-400 focus-within:ring-2 focus-within:ring-sky-100">
            <div className="flex items-start gap-1 py-[6px] pl-3 pr-1">
                <div className="relative min-w-0 flex-1 cursor-text" onMouseDown={(e) => e.target === e.currentTarget && editor.commands.focus("end")}>
                    <EditorContent editor={editor} />
                    {placeholder && editor.isEmpty && (
                        <p className="pointer-events-none absolute left-0 top-0 select-none truncate text-[13.5px] leading-[22px] text-slate-300">
                            {placeholder}
                        </p>
                    )}
                </div>
                <div className="flex shrink-0 items-center gap-0.5 -my-[3px]">
                    <LengthHint length={rendered.length} />
                    <VariableMenu variables={variables} onPick={insert} />
                    <IconBtn title="Insert a condition: show words only when a field matches" onClick={() => editor.chain().focus().insertConditional().run()}>
                        <GitBranchIcon className="h-3.5 w-3.5" />
                    </IconBtn>
                    <IconBtn
                        title="Insert spintax: each send picks one option, so the subject varies"
                        onClick={() => editor.chain().focus().insertContent("{quick question|one idea}").run()}
                    >
                        <ShuffleIcon className="h-3.5 w-3.5" />
                    </IconBtn>
                    {aiSlot}
                </div>
            </div>
            <EditorSuggest editor={editor} variables={variables} forms={false} />
        </div>
    );
});

// Phones show roughly the first 35 to 50 characters of a subject.
function LengthHint({ length }: { length: number }) {
    if (length === 0) return null;
    const tone = length > 60 ? "text-rose-500" : length > 45 ? "text-amber-600" : "text-slate-400";
    const note =
        length > 60
            ? "Long: phones cut this off. Aim for under 45 characters."
            : length > 45
              ? "Getting long: phones show about 35 to 50 characters."
              : "Good length for a cold email subject.";
    return (
        <span title={`${length} characters with sample data. ${note}`} className={`px-1 text-[10.5px] tabular-nums ${tone}`}>
            {length}
        </span>
    );
}

function IconBtn({ title, onClick, children }: { title: string; onClick: () => void; children: React.ReactNode }) {
    return (
        <button
            type="button"
            title={title}
            aria-label={title}
            onMouseDown={(e) => e.preventDefault()}
            onClick={onClick}
            className="inline-flex h-7 w-7 items-center justify-center rounded text-slate-500 transition-colors hover:bg-slate-100 hover:text-slate-900"
        >
            {children}
        </button>
    );
}
