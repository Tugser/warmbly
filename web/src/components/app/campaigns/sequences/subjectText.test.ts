import { afterEach, describe, expect, it } from "vitest";
import { Editor } from "@tiptap/core";
import Document from "@tiptap/extension-document";
import Paragraph from "@tiptap/extension-paragraph";
import Text from "@tiptap/extension-text";
import { VariableNode } from "./nodes/VariableNode";
import { ConditionalNode } from "./nodes/ConditionalNode";
import { oneLine, subjectToHTML } from "./subjectText";

let editor: Editor | null = null;
afterEach(() => editor?.destroy());

// What the subject editor stores after loading a saved subject.
function roundTrip(subject: string): { text: string; chips: string[] } {
    editor = new Editor({
        extensions: [Document.extend({ content: "paragraph" }), Paragraph, Text, VariableNode, ConditionalNode],
        content: subjectToHTML(subject),
    });
    const chips: string[] = [];
    editor.state.doc.descendants((n) => {
        if (n.type.name === "variable" || n.type.name === "conditional") chips.push(n.type.name);
    });
    return { text: oneLine(editor.getText()), chips };
}

describe("subject editor text", () => {
    it("keeps a subject with fields, fallbacks, helpers and spintax exactly as saved", () => {
        const subject = '{quick|one} idea for {{.Company | default "your team"}}, {{.FirstName | title}} & co <3';
        expect(roundTrip(subject).text).toBe(subject);
    });

    it("shows fields and a condition as chips", () => {
        const subject = "{{if .Company}}{{.Company}} hiring{{else}}hiring{{end}} for {{.FirstName}}";
        const { text, chips } = roundTrip(subject);
        expect(text).toBe(subject);
        expect(chips).toEqual(["conditional", "variable"]);
    });

    it("joins pasted lines onto one line", () => {
        expect(oneLine("quick idea\n  for you\r\n")).toBe("quick idea for you ");
    });
});
