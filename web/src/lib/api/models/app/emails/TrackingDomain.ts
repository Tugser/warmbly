// Mirrors backend models.TrackingDomainStatus.
//
// Everything below the verified flag is diagnostic: a bare "Pending DNS" badge
// left customers with nothing to act on, so the backend now says what DNS
// returned and what to do about it.
export default interface TrackingDomain {
    tracking_domain: string;
    tracking_domain_verified: boolean;
    tracking_domain_verified_at?: Date | null;
    // The CNAME value for this install (its TRACKING_DOMAIN). Empty means the
    // install has no tracking host, so nothing can verify.
    cname_target: string;
    // The ownership TXT record a new verification needs beside the CNAME.
    txt_name?: string;
    txt_value?: string;
    // verified | unset | no_target | not_found | wrong_target | proof_missing |
    // lookup_error | pending (stored state that has not been re-resolved).
    status: string;
    // One sentence, safe to render as-is.
    message: string;
    // What DNS actually returned, for comparing against what was typed.
    observed?: string;
    // The record is right but this install's tracking host does not resolve.
    tracking_host_unresolvable: boolean;
}
