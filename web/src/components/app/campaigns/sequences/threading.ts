import type Sequence from "@/lib/api/models/app/campaigns/sequences/Sequence";
import type ABVariant from "@/lib/api/models/app/campaigns/ABVariant";
import type { EarlierEmail } from "./emailAIPrompt";
import { htmlToPlain } from "./emailPreview";
import { stepPorts } from "./campaignGraph";
import { reachableFrom } from "@/components/app/flow/tree";

/**
 * conversationOpenerFor is the step that opened the conversation a step at
 * `index` would reply on. Walking back, every step that also replies passes
 * the question on; the first that does not is the one that opened the thread.
 *
 * `null` means there is no earlier email step, so this one opens the
 * conversation whatever its switch says.
 *
 * It mirrors the walk the send path does over the contact's actual sends, so
 * the composer shows what the recipient will read. It follows the canvas order
 * rather than branch targets: a step several branches reach can be opened by a
 * different email on each path, and the editor has to show one answer. The
 * send path resolves it per contact from what they were actually sent, so this
 * is a preview, and for a linear sequence the two always agree.
 */
export function conversationOpenerFor(steps: Sequence[], index: number): Sequence | null {
    let opener: Sequence | null = null;
    for (let i = index - 1; i >= 0; i--) {
        const step = steps[i];
        if (step.kind !== "email") continue;
        opener = step;
        if (!step.thread_reply) break;
    }
    return opener;
}

/**
 * conversationSubjectFor is the subject a step at `index` would send when it
 * replies in the contact's thread: the opener's own. `null` when there is no
 * earlier email step; an empty string when the opener has no subject yet.
 */
export function conversationSubjectFor(steps: Sequence[], index: number): string | null {
    return conversationOpenerFor(steps, index)?.subject ?? null;
}

/**
 * earlierEmailsFor lists the emails every contact reaching the step at `index`
 * has already been sent, oldest first: the email steps on every path to it from
 * the first step. An email on only some paths (one side of a branch) is left
 * out, because a contact on the other side never read it.
 */
export function earlierEmailsFor(steps: Sequence[], index: number): EarlierEmail[] {
    const target = steps[index];
    const first = steps[0];
    if (!target || !first || target === first) return [];
    const byId = new Map(steps.map((s) => [s.id, s]));
    const ports = (id: string) => (byId.get(id) ? stepPorts(byId.get(id)!) : []);
    const reached = reachableFrom({ ports }, first.id);
    if (!reached.has(target.id)) return [];
    const onEveryPath = (s: Sequence) =>
        s === first || !reachableFrom({ ports: (id) => (id === s.id ? [] : ports(id)) }, first.id).has(target.id);
    // Steps on every path lie on one chain, so each is reachable from those before it.
    const chain = steps.filter((s) => s !== target && s.kind === "email" && reached.has(s.id) && onEveryPath(s));
    const after = new Map(chain.map((s) => [s.id, reachableFrom({ ports }, s.id)]));
    chain.sort((a, b) => (a === b ? 0 : after.get(a.id)!.has(b.id) ? -1 : 1));
    return chain.map((s) => ({ subject: s.subject, body: s.body_plain?.trim() ? s.body_plain : htmlToPlain(s.body_html ?? "") }));
}

export interface ArmSubject {
    name: string;
    subject: string;
}

/**
 * openerArmSubjects lists the subjects the opener's A/B variants send in place
 * of its own, which each contact sent one is replied to under. Mirrors
 * SelectVariant: any active row on the opener (its control included) shadows
 * the campaign-wide variants, and a blank variant subject reuses the step's.
 */
export function openerArmSubjects(opener: Sequence | null, variants: ABVariant[]): ArmSubject[] {
    if (!opener) return [];
    const active = variants.filter((v) => v.is_active);
    const own = active.filter((v) => v.step_id === opener.id);
    const arms = (own.length > 0 ? own : active.filter((v) => !v.step_id)).filter((v) => !v.is_control);
    const seen = new Set([opener.subject]);
    const out: ArmSubject[] = [];
    for (const v of arms) {
        if (!v.subject || seen.has(v.subject)) continue;
        seen.add(v.subject);
        out.push({ name: v.name, subject: v.subject });
    }
    return out;
}
