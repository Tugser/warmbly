// Connection security for a mailbox leg. "tls" is negotiated before the
// greeting, "starttls" upgrades in-band after it, and "none" is no encryption
// at all, which is only legal against a loopback host on a self-hosted
// instance. Mirrors models.MailSecurity* in Go.
export type MailSecurity = "tls" | "starttls" | "none";

export default interface Service {
    username: string;
    password: string;
    host: string;
    port: number;
    /** Omitted means "infer from the port", matching the backend. */
    security?: MailSecurity;
    /** Trust this server on its pinned certificate instead of a certificate authority. */
    tls_unverified?: boolean;
    /** SHA-256 of the pinned certificate's public key, lowercase hex. */
    tls_pin?: string;
}

/** A certificate a mail server presented on a connection test. */
export interface MailCertificate {
    fingerprint: string;
    subject?: string;
    issuer?: string;
    /** RFC 3339 expiry. */
    not_after?: string;
    self_signed?: boolean;
    /** The chain also passes certificate authority verification. */
    trusted?: boolean;
}

export interface MailboxServerCertificate {
    host: string;
    port: number;
    tls_unverified: boolean;
    tls_pin?: string;
    /** Present on a check: what the server shows now. */
    observed?: MailCertificate;
}

export interface MailboxCertificates {
    smtp: MailboxServerCertificate;
    imap: MailboxServerCertificate;
}

/** The error code the backend answers when a server's TLS handshake or certificate failed. */
export const MAILBOX_TLS_FAILED = "mailbox_tls_failed";

/** The reason on a mailbox_tls_failed the owner can resolve by trusting the server's own certificate. */
export const CERTIFICATE_UNTRUSTED = "certificate_untrusted";

/** True when a test failed on a certificate no authority vouches for, not on a missing STARTTLS or a dead port. */
export function offersCertificateTrust(e: unknown): boolean {
    const err = e as { code?: string; reason?: string } | undefined;
    return err?.code === MAILBOX_TLS_FAILED && err.reason === CERTIFICATE_UNTRUSTED;
}

/** Groups a hex fingerprint in pairs for reading aloud and comparing. */
export function formatFingerprint(fp: string): string {
    return (fp.match(/.{1,2}/g) ?? []).join(":").toUpperCase();
}

/** Conventional SMTP default: 465 is implicit TLS, everything else STARTTLS. */
export function defaultSmtpSecurity(port: number): MailSecurity {
    return port === 465 ? "tls" : "starttls";
}

/** Conventional IMAP default: 143 is STARTTLS, everything else implicit TLS. */
export function defaultImapSecurity(port: number): MailSecurity {
    return port === 143 ? "starttls" : "tls";
}

/** Any routable TCP port; the security mode carries how to connect. */
export function validPort(port: number): boolean {
    return Number.isInteger(port) && port > 0 && port <= 65535;
}

/**
 * Whether a host addresses this machine, the only place `none` is allowed.
 *
 * Literals only, matching models.LoopbackMailHost in Go: a name that resolves
 * to 127.0.0.1 today can resolve elsewhere at dial time, so the backend and
 * the worker both refuse anything but a literal and the form must not offer
 * what they would refuse.
 */
export function isLoopbackHost(host: string): boolean {
    const h = host.trim().toLowerCase();
    if (h === "localhost") return true;
    const bare = h.startsWith("[") && h.endsWith("]") ? h.slice(1, -1) : h;
    if (/^127(\.\d{1,3}){3}$/.test(bare)) {
        return bare.split(".").every((o) => Number(o) <= 255);
    }
    return bare === "::1" || bare === "0:0:0:0:0:0:0:1";
}

/**
 * Whether the unencrypted mode is offerable at all: a loopback host on a
 * self-hosted instance. On the hosted product the worker is never the
 * customer's machine, so there is no local relay for it to reach.
 */
export function allowsNoEncryption(host: string, selfHosted: boolean): boolean {
    return selfHosted && isLoopbackHost(host);
}
