// SendAdditions sits inside the email frame, under the body, and shows what the
// send adds to every email as it arrives: the sending mailbox's signature, then
// the campaign's opt-out line. Nobody then writes a second signature into the copy.

import { Link } from "@tanstack/react-router";
import EmailBody from "@/components/app/unibox/EmailBody";
import type Inbox from "@/lib/api/models/app/emails/Inbox";
import type Campaign from "@/lib/api/models/app/campaigns/Campaign";
import { useOutreachSettings } from "@/lib/api/hooks/app/outreach/useOutreachSettings";
import { PreviewMailboxPicker } from "./PreviewControls";
import { effectiveOptOut } from "./optOutLine";

const signs = (i: Inbox) => i.signature_sync && (i.signature_html.trim() !== "" || i.signature_plain.trim() !== "");

export default function SendAdditions({
    inboxes,
    mailbox,
    onMailboxChange,
    campaign,
}: {
    // The mailboxes this email can go out from.
    inboxes: Inbox[];
    mailbox: Inbox | null;
    onMailboxChange: (i: Inbox) => void;
    // Null in the new-campaign wizard, before the campaign exists.
    campaign: Pick<Campaign, "id" | "unsubscribe_mode" | "text_only"> | null;
}) {
    const { data: outreach } = useOutreachSettings();
    const optOut = effectiveOptOut(outreach?.unsubscribe, campaign?.unsubscribe_mode);
    if (!mailbox && !optOut) return null;
    const on = !!mailbox && signs(mailbox);
    const signed = inboxes.filter(signs).length;
    const plainOnly = !!campaign?.text_only;

    return (
        <div className="rounded-b-md border-t border-dashed border-slate-200 bg-slate-50/60 px-3 pb-3 pt-2">
            <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                <span className="text-[10px] font-medium uppercase tracking-[0.14em] text-slate-400">Added when sent</span>
                <span className="text-[11px] text-slate-400">You don't write these; every email gets them.</span>
                <span className="ml-auto flex items-center gap-1">
                    {inboxes.length > 1 && <PreviewMailboxPicker inboxes={inboxes} value={mailbox} onChange={onMailboxChange} />}
                    {mailbox && (
                        <Link
                            to="/app/emails"
                            search={{ mailbox: mailbox.id, tab: "settings" }}
                            className="h-6 px-1.5 inline-flex items-center rounded text-[11.5px] font-medium text-sky-600 hover:bg-sky-50 hover:text-sky-700"
                        >
                            {on ? "Edit signature" : "Add a signature"}
                        </Link>
                    )}
                </span>
            </div>

            {mailbox &&
                (on ? (
                    <div className="mt-1.5 select-none opacity-80" aria-label={`Signature of ${mailbox.email}`}>
                        {plainOnly ? (
                            <p className="whitespace-pre-wrap text-[13px] leading-relaxed text-slate-700">{mailbox.signature_plain}</p>
                        ) : (
                            <EmailBody html={mailbox.signature_html} plain={mailbox.signature_plain} />
                        )}
                    </div>
                ) : (
                    <p className="mt-1.5 text-[12px] italic text-slate-400">
                        {mailbox.signature_html.trim() || mailbox.signature_plain.trim()
                            ? "This mailbox's signature is turned off, so none is added."
                            : "No signature on this mailbox yet, so none is added."}
                    </p>
                ))}

            {optOut && (
                <p className="mt-2 text-[12px] text-slate-500">
                    {optOut.mode === "link" ? (
                        <>
                            {optOut.link_intro} <span className="underline underline-offset-2">{optOut.link_text}</span>
                        </>
                    ) : (
                        optOut.text
                    )}
                    {campaign && (
                        <Link
                            to="/app/campaigns/$id/preferences"
                            params={{ id: campaign.id }}
                            className="ml-1.5 text-[11px] text-sky-600 hover:text-sky-700"
                        >
                            Change
                        </Link>
                    )}
                </p>
            )}

            {inboxes.length > 1 && (
                <p className="mt-2 text-[10.5px] leading-relaxed text-slate-400">
                    Each email signs with the mailbox that sends it; {signed} of {inboxes.length} have a signature. To set one on many
                    mailboxes at once, select them on the Accounts page and choose Signature.
                </p>
            )}
        </div>
    );
}
