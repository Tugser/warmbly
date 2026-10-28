import Request from "../../Request";
import type { MailboxCertificates } from "@/lib/api/models/app/emails/Service";

/** The certificate each server of an SMTP/IMAP mailbox is trusted on. */
export async function getMailboxCertificates(mailboxId: string, signal?: AbortSignal): Promise<MailboxCertificates> {
    return await Request<MailboxCertificates>({
        method: "GET",
        url: `/emails/${mailboxId}/certificates`,
        authorization: true,
        signal,
    });
}

/** Runs a connection test and reports what each server presents now. Changes nothing. */
export async function checkMailboxCertificates(mailboxId: string): Promise<MailboxCertificates> {
    return await Request<MailboxCertificates>({
        method: "POST",
        url: `/emails/onboarding/smtp-imap/${mailboxId}/certificates/check`,
        authorization: true,
    });
}

/** Pins the fingerprints the owner confirmed, after a test against exactly those. */
export async function confirmMailboxCertificates(
    mailboxId: string,
    pins: { smtp_pin?: string; imap_pin?: string },
): Promise<MailboxCertificates> {
    return await Request<MailboxCertificates>({
        method: "POST",
        url: `/emails/onboarding/smtp-imap/${mailboxId}/certificates/confirm`,
        data: pins,
        authorization: true,
    });
}
