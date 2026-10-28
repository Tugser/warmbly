import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { RefreshCwIcon, ShieldAlertIcon, ShieldCheckIcon } from "lucide-react";
import toast from "react-hot-toast/headless";

import { useConfirm } from "@/hooks/context/confirm";
import { usePermission } from "@/hooks/usePermission";
import type { AppError } from "@/lib/api/client/normalizeError";
import {
    checkMailboxCertificates,
    confirmMailboxCertificates,
    getMailboxCertificates,
} from "@/lib/api/client/app/emails/mailboxCertificates";
import {
    formatFingerprint,
    type MailboxCertificates,
    type MailboxServerCertificate,
    type MailCertificate,
} from "@/lib/api/models/app/emails/Service";
import buildError from "@/lib/helper/buildError";

// How each server of an SMTP/IMAP mailbox is trusted: by a certificate
// authority, or on the certificate the owner confirmed.

const LEGS = [
    { key: "smtp", label: "Sending (SMTP)" },
    { key: "imap", label: "Receiving (IMAP)" },
] as const;

function describe(label: string, c: MailCertificate): string {
    const parts = [`${label}: SHA-256 ${formatFingerprint(c.fingerprint)}`];
    if (c.subject) parts.push(`for ${c.subject}`);
    if (c.self_signed) parts.push("self-signed");
    else if (c.issuer) parts.push(`issued by ${c.issuer}`);
    if (c.not_after) parts.push(`valid until ${new Date(c.not_after).toLocaleDateString()}`);
    return parts.join(", ");
}

function ServerRow({ label, server }: { label: string; server: MailboxServerCertificate }) {
    const pinned = server.tls_unverified && !!server.tls_pin;
    return (
        <div className="flex items-start gap-2">
            {server.tls_unverified ? (
                <ShieldAlertIcon className="w-3.5 h-3.5 text-amber-600 shrink-0 mt-0.5" />
            ) : (
                <ShieldCheckIcon className="w-3.5 h-3.5 text-emerald-600 shrink-0 mt-0.5" />
            )}
            <div className="min-w-0">
                <div className="text-[12.5px] font-medium text-slate-900">
                    {label}{" "}
                    <span className="font-normal text-slate-500">
                        {server.host}:{server.port}
                    </span>
                </div>
                <div className="text-[11px] text-slate-500">
                    {!server.tls_unverified
                        ? "Verified by a certificate authority."
                        : pinned
                          ? "Trusted on its own certificate. Warmbly stops connecting if it changes."
                          : "Trusted on its own certificate, which is not confirmed yet."}
                </div>
                {pinned && (
                    <div className="mt-1 font-mono text-[10.5px] text-slate-500 break-all">
                        SHA-256 {formatFingerprint(server.tls_pin!)}
                    </div>
                )}
            </div>
        </div>
    );
}

export default function ServerCertificatesCard({ mailboxId }: { mailboxId: string }) {
    const queryClient = useQueryClient();
    const confirm = useConfirm();
    const canManage = usePermission("MANAGE_EMAILS");
    const [checking, setChecking] = useState(false);
    const queryKey = ["emails", "certificates", mailboxId];
    const { data, isLoading } = useQuery({
        queryKey,
        queryFn: ({ signal }) => getMailboxCertificates(mailboxId, signal),
    });

    if (isLoading || !data) return null;
    const optedOut = data.smtp.tls_unverified || data.imap.tls_unverified;

    const reconfirm = async () => {
        setChecking(true);
        let seen: MailboxCertificates;
        try {
            seen = await checkMailboxCertificates(mailboxId);
        } catch (e) {
            toast.error(buildError(e as AppError));
            return;
        } finally {
            setChecking(false);
        }
        const legs = LEGS.filter((l) => seen[l.key].tls_unverified);
        const missing = legs.find((l) => !seen[l.key].observed?.fingerprint);
        if (missing) {
            toast.error(`The ${missing.label} server did not present a certificate Warmbly could read. Try again shortly.`);
            return;
        }
        const lines = legs.map((l) => describe(l.label, seen[l.key].observed!)).join(". ");
        confirm.show(
            `Trust these certificates for ${legs.map((l) => l.label).join(" and ")}? ${lines}. Compare them with what the server's owner gives you before confirming.`,
            async () => {
                try {
                    await confirmMailboxCertificates(mailboxId, {
                        smtp_pin: seen.smtp.tls_unverified ? seen.smtp.observed?.fingerprint : undefined,
                        imap_pin: seen.imap.tls_unverified ? seen.imap.observed?.fingerprint : undefined,
                    });
                    toast.success("Certificates confirmed");
                } catch (e) {
                    toast.error(buildError(e as AppError));
                } finally {
                    await Promise.all([
                        queryClient.invalidateQueries({ queryKey }),
                        queryClient.invalidateQueries({ queryKey: ["emails", "list"] }),
                        queryClient.invalidateQueries({ queryKey: ["analytics", "accounts"] }),
                    ]);
                }
            },
        );
    };

    return (
        <div className="px-5 py-5 space-y-3">
            <div className="text-[10px] uppercase tracking-[0.14em] text-slate-400 font-medium">Server certificates</div>
            <div className="space-y-3">
                {LEGS.map((l) => (
                    <ServerRow key={l.key} label={l.label} server={data[l.key]} />
                ))}
            </div>
            {optedOut && canManage && (
                <div className="flex items-center justify-between gap-3">
                    <div className="text-[11px] text-slate-400 min-w-0">
                        If the server's owner replaced a certificate, run a test and confirm the new one.
                    </div>
                    <button
                        type="button"
                        onClick={reconfirm}
                        disabled={checking}
                        className="h-7 px-2.5 shrink-0 inline-flex items-center gap-1.5 rounded-md border border-slate-200 hover:border-slate-300 bg-white text-slate-700 text-[12px] font-medium disabled:opacity-60 transition-colors"
                    >
                        <RefreshCwIcon className={checking ? "w-3.5 h-3.5 animate-spin" : "w-3.5 h-3.5"} />
                        {checking ? "Testing…" : "Re-confirm"}
                    </button>
                </div>
            )}
        </div>
    );
}
