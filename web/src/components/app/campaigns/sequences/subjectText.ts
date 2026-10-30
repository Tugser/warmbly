// The subject is stored as plain text; the editor shows it with chips.

import { FIELD_TOKEN_RE } from "@/lib/templateVars";

// One conditional with no nested {{if}}, the shape the condition chip builds.
const CONDITIONAL_RE = /\{\{\s*if\s[^}]*\}\}(?:(?!\{\{\s*(?:if|end)\b)[\s\S])*?\{\{\s*end\s*\}\}/g;

export const escapeHTML = (s: string) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");

// subjectInline is the editor's view of a stored subject: text, with each
// field token and simple conditional upgraded to its chip.
export function subjectInline(text: string): string {
    const fields = (s: string) =>
        escapeHTML(s).replace(new RegExp(FIELD_TOKEN_RE.source, "g"), (tok) => `<span data-var="">${tok}</span>`);
    let out = "";
    let last = 0;
    for (const m of text.matchAll(CONDITIONAL_RE)) {
        out += fields(text.slice(last, m.index)) + `<span data-if="">${escapeHTML(m[0])}</span>`;
        last = (m.index ?? 0) + m[0].length;
    }
    return out + fields(text.slice(last));
}

export const subjectToHTML = (text: string) => `<p>${subjectInline(text)}</p>`;

export const oneLine = (s: string) => s.replace(/\s*[\r\n]+\s*/g, " ");

