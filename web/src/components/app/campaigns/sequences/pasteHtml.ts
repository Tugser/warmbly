// Paste normaliser for the campaign body editor (issue #380).
//
// Copying a message out of Gmail, Outlook or Word brings its own idea of a
// blank line with it: Gmail writes one as `<div><br></div>`, Word as an empty
// `<p class=MsoNormal>` holding `&nbsp;`. Our paragraphs already carry their
// own bottom margin, so pasting those verbatim renders every gap twice — the
// reported double spacing. This strips the source's spacing scaffolding (and
// its editor cruft) and leaves the structure: paragraphs, breaks, lists,
// headings, links, images and the inline marks the schema keeps.

// Elements whose content must not survive a paste: a <style> block's CSS is
// text to the parser and would land in the body as copy.
const DROP_WITH_CONTENT = new Set(["STYLE", "SCRIPT", "META", "LINK", "TITLE", "HEAD", "NOSCRIPT"]);

// Blocks that render their own gap in our editor, so an empty one is spacing
// the source drew by hand and we draw with CSS.
const SPACING_BLOCKS = new Set(["P", "DIV", "H1", "H2", "H3", "H4", "H5", "H6"]);

// An image src a mail client can actually load. A `cid:` part belongs to the
// message it was copied from and a `data:` blob is stripped by most clients, so
// both would only ever render as a broken image for the recipient.
function isLoadableImage(src: string): boolean {
    return /^https?:\/\//i.test(src.trim());
}

// isBlankBlock reports whether a block holds nothing but whitespace, <br> and
// non-breaking spaces — the shapes every mail client writes a blank line as.
function isBlankBlock(el: Element): boolean {
    if (el.querySelector("img, table, hr, iframe, video")) return false;
    // U+00A0 is the &nbsp; Word fills its spacer paragraphs with.
    return (el.textContent ?? "").replace(/[\s\u00a0]+/g, "") === "";
}

// stripTrailingBreaks drops the <br>s a block ends with. Gmail closes a line
// with one, and it renders as an extra blank line inside a paragraph that
// already has a margin under it.
function stripTrailingBreaks(el: Element) {
    let last = el.lastChild;
    while (last) {
        if (last.nodeType === Node.TEXT_NODE && (last.textContent ?? "").replace(/[\s\u00a0]+/g, "") === "") {
            const prev = last.previousSibling;
            last.parentNode?.removeChild(last);
            last = prev;
            continue;
        }
        if (last.nodeType === Node.ELEMENT_NODE && (last as Element).tagName === "BR") {
            const prev = last.previousSibling;
            last.parentNode?.removeChild(last);
            last = prev;
            continue;
        }
        break;
    }
}

// collapseBreakRuns turns a run of consecutive <br>s into one. Two in a row is
// how a mail client writes a paragraph break inside a block; keeping both would
// stack an empty line on top of the margin our paragraphs already have.
function collapseBreakRuns(root: ParentNode) {
    for (const br of Array.from(root.querySelectorAll("br"))) {
        let next = br.nextSibling;
        while (next) {
            if (next.nodeType === Node.TEXT_NODE && (next.textContent ?? "").replace(/[\s\u00a0]+/g, "") === "") {
                const after = next.nextSibling;
                next.parentNode?.removeChild(next);
                next = after;
                continue;
            }
            if (next.nodeType === Node.ELEMENT_NODE && (next as Element).tagName === "BR") {
                const after = next.nextSibling;
                next.parentNode?.removeChild(next);
                next = after;
                continue;
            }
            break;
        }
    }
}

// unwrap replaces an element with its children, keeping the content.
function unwrap(el: Element) {
    const parent = el.parentNode;
    if (!parent) return;
    while (el.firstChild) parent.insertBefore(el.firstChild, el);
    parent.removeChild(el);
}

// Chip spans our own nodes serialize to. A copy from one step's body into
// another arrives as ordinary HTML, so these have to survive the span unwrap or
// the merge fields, AI blocks, conditions and form links land as literal text.
const KEEP_SPAN_ATTRS = ["data-var", "data-ai-var", "data-if", "data-form-link"];

// Inline styling worth keeping off a pasted <span>. The schema holds far more
// than this, but a paste out of Gmail or Word wraps every run of text in the
// source editor's own font stack and size, and importing those makes a cold
// email render in Arial 13px in every inbox. A colour or a highlight is a
// choice someone made; a font-family is the tool they happened to use.
// The shorthand is normalised to the longhand the schema reads.
const KEEP_SPAN_STYLES: Record<string, string> = {
    color: "color",
    "background-color": "background-color",
    background: "background-color",
};

// Near-black is the source's default text colour, not a decision, and a body
// that pins it renders black on a reader's dark theme.
const DEFAULT_TEXT_COLOURS = new Set([
    "black", "#000", "#000000", "#111", "#111111", "#222", "#222222", "#333", "#333333",
    "rgb(0,0,0)", "rgb(17,17,17)", "rgb(34,34,34)", "rgb(51,51,51)",
]);

// isColourValue accepts a single colour token: a name, a hex code, or one
// functional form. Anything with a second top-level token is a shorthand
// carrying more than a colour.
function isColourValue(value: string): boolean {
    const v = value.trim();
    if (!v || /url\(|gradient/i.test(v)) return false;
    let depth = 0;
    for (const ch of v) {
        if (ch === "(") depth++;
        else if (ch === ")") depth = Math.max(0, depth - 1);
        else if (/\s/.test(ch) && depth === 0) return false;
    }
    return true;
}

function isDefaultColour(value: string): boolean {
    return DEFAULT_TEXT_COLOURS.has(value.replace(/\s+/g, "").toLowerCase());
}

// Named colours light enough to vanish on a white page, plus the two that mean
// "no colour at all". Any other name is a choice and is kept.
const NAMED_RGB: Record<string, [number, number, number]> = {
    white: [255, 255, 255], snow: [255, 250, 250], ivory: [255, 255, 240], whitesmoke: [245, 245, 245],
    ghostwhite: [248, 248, 255], azure: [240, 255, 255], mintcream: [245, 255, 250], honeydew: [240, 255, 240],
    aliceblue: [240, 248, 255], seashell: [255, 245, 238], floralwhite: [255, 250, 240], linen: [250, 240, 230],
    oldlace: [253, 245, 230], lavenderblush: [255, 240, 245], beige: [245, 245, 220], cornsilk: [255, 248, 220],
    lightyellow: [255, 255, 224], lightcyan: [224, 255, 255], gainsboro: [220, 220, 220],
    lightgray: [211, 211, 211], lightgrey: [211, 211, 211], silver: [192, 192, 192],
    black: [0, 0, 0],
};

// parseColour reads a CSS colour into [r, g, b, alpha], or null when it is a
// form we do not judge (an unlisted name, a variable, currentColor).
export function parseColour(value: string): [number, number, number, number] | null {
    const v = value.trim().toLowerCase().replace(/\s*!important$/, "");
    if (v === "transparent") return [0, 0, 0, 0];
    if (NAMED_RGB[v]) return [...NAMED_RGB[v], 1];
    const hex = /^#([0-9a-f]{3,8})$/.exec(v)?.[1];
    if (hex) {
        const full = hex.length <= 4 ? [...hex].map((c) => c + c).join("") : hex;
        if (full.length !== 6 && full.length !== 8) return null;
        const n = (i: number) => parseInt(full.slice(i, i + 2), 16);
        return [n(0), n(2), n(4), full.length === 8 ? n(6) / 255 : 1];
    }
    const fn = /^(rgba?|hsla?)\((.*)\)$/.exec(v);
    if (!fn) return null;
    const parts = fn[2].split(/[\s,/]+/).filter(Boolean);
    if (parts.length < 3) return null;
    const alpha = parts[3] === undefined ? 1 : parts[3].endsWith("%") ? parseFloat(parts[3]) / 100 : parseFloat(parts[3]);
    if (fn[1].startsWith("rgb")) {
        const ch = (p: string) => (p.endsWith("%") ? (parseFloat(p) * 255) / 100 : parseFloat(p));
        const rgb = parts.slice(0, 3).map(ch);
        return rgb.some(Number.isNaN) || Number.isNaN(alpha) ? null : [rgb[0], rgb[1], rgb[2], alpha];
    }
    const h = parseFloat(parts[0]);
    const s = parseFloat(parts[1]) / 100;
    const l = parseFloat(parts[2]) / 100;
    if ([h, s, l, alpha].some(Number.isNaN)) return null;
    const k = (n: number) => (n + h / 30) % 12;
    const a = s * Math.min(l, 1 - l);
    const f = (n: number) => 255 * (l - a * Math.max(-1, Math.min(k(n) - 3, 9 - k(n), 1)));
    return [f(0), f(8), f(4), alpha];
}

// luminance is WCAG relative luminance, with any transparency laid over white.
function luminance([r, g, b, a]: [number, number, number, number]): number {
    const lin = (c: number) => {
        const s = (a * c + (1 - a) * 255) / 255;
        return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
    };
    return 0.2126 * lin(r) + 0.7152 * lin(g) + 0.0722 * lin(b);
}

// A text colour that cannot be read on the white page a cold email is written
// and mostly read on: the near-white body text of a dark-themed site or of
// Gmail's dark mode, which pasted as invisible lines. Contrast under 2.5:1.
export function isUnreadableTextColour(value: string): boolean {
    const c = parseColour(value);
    return !!c && 1.05 / (luminance(c) + 0.05) < 2.5;
}

// A background that is page chrome rather than a highlight: transparent, or
// dark enough that body text would not read on it (under 4.5:1 for slate-800).
export function isPageBackground(value: string): boolean {
    const c = parseColour(value);
    if (!c) return false;
    return c[3] === 0 || (luminance(c) + 0.05) / 0.072 < 4.5;
}

// keptInlineStyle reduces an element's style attribute to the declarations
// above, or "" when nothing in it was a choice.
// The declarations of a style attribute, split on the semicolons between
// them and not on one inside url(...) or a quoted value.
function splitDeclarations(style: string): string[] {
    const out: string[] = [];
    let depth = 0;
    let quote = "";
    let start = 0;
    for (let i = 0; i < style.length; i++) {
        const ch = style[i];
        if (quote) {
            if (ch === "\\") i++;
            else if (ch === quote) quote = "";
        } else if (ch === '"' || ch === "'") quote = ch;
        else if (ch === "(") depth++;
        else if (ch === ")") depth = Math.max(0, depth - 1);
        else if (ch === ";" && depth === 0) {
            out.push(style.slice(start, i));
            start = i + 1;
        }
    }
    out.push(style.slice(start));
    return out;
}

function keptInlineStyle(el: Element): string {
    const kept = new Map<string, string>();
    for (const decl of splitDeclarations(el.getAttribute("style") ?? "")) {
        const at = decl.indexOf(":");
        if (at < 0) continue;
        const prop = KEEP_SPAN_STYLES[decl.slice(0, at).trim().toLowerCase()];
        const value = decl.slice(at + 1).trim();
        if (!prop || !value) continue;
        if (prop === "color" && (isDefaultColour(value) || isUnreadableTextColour(value))) continue;
        // The background shorthand is only a highlight when it is nothing but
        // a colour. Word and Outlook paste "background: yellow none repeat
        // scroll 0% 0%", and copying that whole value into the longhand writes
        // a declaration every client drops, losing the highlight entirely.
        if (prop === "background-color" && (!isColourValue(value) || isPageBackground(value))) continue;
        kept.set(prop, value);
    }
    // <font color> says the same thing in the older spelling.
    const fontColour = (el.getAttribute("color") ?? "").trim();
    if (fontColour && !kept.has("color") && !isDefaultColour(fontColour) && !isUnreadableTextColour(fontColour)) {
        kept.set("color", fontColour);
    }
    return [...kept].map(([prop, value]) => `${prop}: ${value}`).join("; ");
}

// Text blocks whose typography is the copied page's, not the author's. A table
// cell is left alone: in a pasted layout its font is part of the design.
const TEXT_BLOCKS = new Set(["P", "DIV", "H1", "H2", "H3", "H4", "H5", "H6", "LI", "UL", "OL", "BLOCKQUOTE"]);

// What a browser writes onto a copied block from the page's computed style.
// Kept, it pins the site's font and size into every inbox the email reaches.
const PAGE_TYPOGRAPHY = /^(font-family|font-size|font-style|font-weight|font-variant.*|font-feature-settings|font-kerning|line-height|letter-spacing|word-spacing|text-indent|text-transform|white-space|orphans|widows|caret-color|text-decoration-(thickness|style|color)|-webkit-.*)$/;

// cleanBlockStyle reduces a block's style attribute the way keptInlineStyle
// does a span's: no colour that would vanish on white, no dark page
// background, and on a text block none of the page's typography.
function cleanBlockStyle(el: Element) {
    const style = el.getAttribute("style");
    if (style === null) return;
    const textBlock = TEXT_BLOCKS.has(el.tagName);
    const kept: string[] = [];
    for (const decl of splitDeclarations(style)) {
        const at = decl.indexOf(":");
        if (at < 0) continue;
        const prop = decl.slice(0, at).trim().toLowerCase();
        const value = decl.slice(at + 1).trim();
        if (!prop || !value) continue;
        if (prop === "color" && (isDefaultColour(value) || isUnreadableTextColour(value))) continue;
        if ((prop === "background-color" || prop === "background") && isColourValue(value) && isPageBackground(value)) continue;
        if (textBlock && PAGE_TYPOGRAPHY.test(prop)) continue;
        kept.push(`${prop}: ${value}`);
    }
    if (kept.length) el.setAttribute("style", kept.join("; "));
    else el.removeAttribute("style");
}

export function normalizePastedHTML(html: string): string {
    if (!html || typeof window === "undefined" || typeof DOMParser === "undefined") return html;
    // A copy from inside a TipTap editor is already a document in our own
    // schema; ProseMirror marks it and re-parses it exactly. Nothing to fix.
    if (html.includes("data-pm-slice")) return html;

    const doc = new DOMParser().parseFromString(html, "text/html");
    const body = doc.body;
    if (!body) return html;

    // Comments carry Word's conditional markup, which is a second copy of the
    // document the parser would otherwise leave lying in the output.
    const walker = doc.createTreeWalker(body, NodeFilter.SHOW_COMMENT);
    const comments: Comment[] = [];
    while (walker.nextNode()) comments.push(walker.currentNode as Comment);
    for (const c of comments) c.parentNode?.removeChild(c);

    for (const el of Array.from(body.querySelectorAll("*"))) {
        if (!el.isConnected) continue;
        const tag = el.tagName;
        if (DROP_WITH_CONTENT.has(tag)) {
            el.remove();
            continue;
        }
        // Word's namespaced elements (<o:p>, <w:sdt>) hold nothing our editor
        // can use but do hold the &nbsp; that makes a spacer paragraph look
        // non-empty, so they go with their content.
        if (tag.includes(":")) {
            el.remove();
            continue;
        }
        if (tag === "IMG") {
            const src = el.getAttribute("src") ?? "";
            if (!isLoadableImage(src)) el.remove();
            continue;
        }
        // A <font> or <span> is kept only for the styling that was a choice:
        // a colour or a highlight becomes a plain <span style>, and a wrapper
        // holding nothing but the source editor's font stack is unwrapped.
        if (tag === "FONT" || tag === "SPAN" || tag === "CENTER") {
            if (KEEP_SPAN_ATTRS.some((a) => el.hasAttribute(a))) continue;
            const style = tag === "CENTER" ? "" : keptInlineStyle(el);
            if (!style) {
                unwrap(el);
                continue;
            }
            const span = el.ownerDocument.createElement("span");
            span.setAttribute("style", style);
            while (el.firstChild) span.appendChild(el.firstChild);
            el.replaceWith(span);
            continue;
        }
        cleanBlockStyle(el);
    }

    collapseBreakRuns(body);

    // Blank spacing blocks, innermost first, so a wrapper that only held one
    // becomes blank in turn and goes with it.
    for (const el of Array.from(body.querySelectorAll("p, div, h1, h2, h3, h4, h5, h6")).reverse()) {
        if (!el.isConnected) continue;
        if (SPACING_BLOCKS.has(el.tagName) && isBlankBlock(el)) el.remove();
    }

    for (const el of Array.from(body.querySelectorAll("p, div, li, h1, h2, h3, h4, h5, h6, td"))) {
        stripTrailingBreaks(el);
    }

    return body.innerHTML;
}
