// Prompts and parsers for writing a whole campaign email, or its subject, with
// AI. The system prompt (internal/pkg/generation/voice.go) carries the voice
// rules; this adds what only the editor knows: where the step sits in the
// sequence and what the prospect has already been sent.

// An email the prospect received before this step, as plain text.
export interface EarlierEmail {
    subject: string;
    body: string;
}

export interface EmailAIContext {
    // Earlier email steps in send order, oldest first.
    earlier: EarlierEmail[];
    // The step replies in the contact's thread, so it has no subject of its own.
    threaded: boolean;
}

// Each earlier email is trimmed so a long sequence cannot crowd out the brief.
const EARLIER_MAX = 1200;
const DRAFT_MAX = 2500;

const MERGE_HINT =
    'Personalize with these merge variables, written exactly like this: {{.FirstName}}, {{.Company}}. Greet with {{.FirstName | default "there"}} so an empty name still reads well, and do not invent other variables.';

function clip(text: string, max: number): string {
    const t = text.trim();
    return t.length > max ? `${t.slice(0, max)}…` : t;
}

function earlierBlock(earlier: EarlierEmail[]): string {
    return earlier
        .map((e, i) => {
            const subject = e.subject.trim() ? ` (subject: ${e.subject.trim()})` : "";
            return `--- Email ${i + 1}${subject}\n${clip(e.body, EARLIER_MAX) || "(empty)"}`;
        })
        .join("\n\n");
}

// stepLabel names the step for the UI: "First email" or "Follow-up 2".
export function stepLabel(ctx: EmailAIContext): string {
    return ctx.earlier.length === 0 ? "First email" : `Follow-up ${ctx.earlier.length}`;
}

export function draftPrompt({
    brief,
    ctx,
    wantSubject,
    currentBody,
}: {
    brief: string;
    ctx: EmailAIContext;
    wantSubject: boolean;
    // The step's current body as plain text, "" when empty.
    currentBody: string;
}): string {
    const parts: string[] = [];
    const n = ctx.earlier.length;
    if (n === 0) {
        parts.push("Write the first cold email of an outreach sequence.");
    } else {
        parts.push(
            `Write follow-up ${n} of an outreach sequence (email ${n + 1}). Follow the FOLLOW-UPS rules.` +
                (ctx.threaded
                    ? " It is sent as a reply in the same thread as the earlier emails."
                    : " It is sent as a new email, not as a reply."),
        );
        parts.push(`Emails the prospect already received, oldest first. Do not repeat them:\n${earlierBlock(ctx.earlier)}`);
    }
    const draft = currentBody.trim();
    if (brief.trim()) {
        parts.push(`What the sender wants this email to do: ${brief.trim()}`);
        if (draft) parts.push(`The current draft, for its facts only (write new wording):\n${clip(draft, DRAFT_MAX)}`);
    } else if (draft) {
        parts.push(
            `Rewrite this draft into a better version that follows the rules. Keep its offer and every fact in it, change the wording:\n${clip(draft, DRAFT_MAX)}`,
        );
    } else if (n > 0) {
        parts.push("Write it from the earlier emails and what you know about the sender.");
    }
    parts.push(MERGE_HINT);
    parts.push("Separate paragraphs with one blank line. Do not add a signature: the mailbox signature is appended automatically.");
    parts.push(
        wantSubject
            ? 'Return the subject on the first line as "Subject: <subject>", then a blank line, then the body. Nothing else.'
            : "Return only the body. No subject line.",
    );
    return parts.join("\n\n");
}

// Moves offered on an existing subject, each a one-click rewrite.
export const SUBJECT_MOVES = [
    { label: "Shorter", goal: "Make it shorter: two to four words." },
    { label: "More personal", goal: "Make it about them: use {{.FirstName}} or {{.Company}}, with a fallback." },
    { label: "As a question", goal: "Turn it into a short, specific question about their business." },
    { label: "Less salesy", goal: "Make it sound like a colleague wrote it: plain words, no pitch, no hype." },
] as const;

export function subjectsPrompt({
    ctx,
    body,
    brief,
    current = "",
    goal = "",
    fields = [],
}: {
    ctx: EmailAIContext;
    body: string;
    brief: string;
    // The subject in the field now; with a goal, the ideas rewrite it.
    current?: string;
    goal?: string;
    // The workspace's custom contact fields, e.g. ["industry"].
    fields?: string[];
}): string {
    const parts: string[] = [];
    if (current.trim() && goal) {
        parts.push(`Rewrite this cold email subject line 5 different ways: ${current.trim()}\nGoal: ${goal}`);
    } else {
        parts.push("Suggest 5 subject lines for this cold email, each a different approach (their problem, a question, a specific observation, a plain two-word topic, a result they want).");
    }
    parts.push(
        [
            "Rules for every subject:",
            "- Under 7 words and 45 characters. Lowercase is fine. It should look like a colleague sent it, not a marketing team.",
            "- About the prospect or their business, never a pitch of the sender's product. Specific beats clever.",
            "- No clickbait, no ALL CAPS, no exclamation marks, no emojis, no fake \"Re:\" or \"Fwd:\", no spam words (free, guarantee, act now, limited time, %).",
            '- Use a merge field in at most two of the five, written exactly like this and always with a fallback so an empty field still reads well: {{.FirstName | default "there"}}, {{.Company | default "your team"}}.',
        ].join("\n"),
    );
    const custom = fields.filter(Boolean).slice(0, 12);
    if (custom.length) parts.push(`The contact list also has these fields, usable the same way: ${custom.map((k) => `{{.${k}}}`).join(", ")}`);
    if (ctx.earlier.length > 0) {
        parts.push(
            `It is follow-up ${ctx.earlier.length}, sent as a new email, so it must not repeat an earlier subject. Earlier subjects: ${
                ctx.earlier.map((e) => e.subject.trim()).filter(Boolean).join(" / ") || "(none)"
            }`,
        );
    }
    if (body.trim()) parts.push(`The email:\n${clip(body, DRAFT_MAX)}`);
    if (brief.trim()) parts.push(`What the sender wants from the subject: ${brief.trim()}`);
    parts.push("Return exactly 5 lines, one subject per line. No numbering, no quotes, no bullets, nothing else.");
    return parts.join("\n\n");
}

function stripFence(text: string): string {
    return text.trim().replace(/^```[a-z]*\n?/i, "").replace(/\n?```$/, "").trim();
}

function unquote(s: string): string {
    return s.trim().replace(/^["'“‘](.*)["'”’]$/, "$1").trim();
}

// parseDraft splits a "Subject: ..." first line off a generated email. A model
// that ignored the format still yields a usable body.
export function parseDraft(text: string): { subject: string | null; body: string } {
    const lines = stripFence(text).split("\n");
    let subject: string | null = null;
    const first = lines.findIndex((l) => l.trim() !== "");
    const m = first >= 0 ? /^\s*\**\s*subject(?: line)?\s*\**\s*:\s*\**(.*?)\**\s*$/i.exec(lines[first]) : null;
    if (m) {
        subject = unquote(m[1]) || null;
        lines.splice(0, first + 1);
    }
    let body = lines.join("\n").trim();
    body = body.replace(/^\**\s*body\s*\**\s*:\s*\n?/i, "").trim();
    return { subject, body };
}

// parseSubjects reads one suggestion per line, dropping list markers, labels,
// quotes and repeats.
export function parseSubjects(text: string): string[] {
    const seen = new Set<string>();
    const out: string[] = [];
    for (const raw of stripFence(text).split("\n")) {
        const s = unquote(
            raw
                .replace(/^\s*(?:[-*•]|\d+[.)])\s*/, "")
                .replace(/^\s*subject(?: line)?\s*:\s*/i, "")
                .replace(/\*\*/g, ""),
        );
        if (!s || s.length > 90 || seen.has(s.toLowerCase())) continue;
        seen.add(s.toLowerCase());
        out.push(s);
    }
    return out.slice(0, 5);
}
