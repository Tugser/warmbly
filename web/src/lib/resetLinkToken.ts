// The emailed reset link carries its token in the fragment; mail sent before that carries it in the query.
export function resetLinkToken(hash: string, search: URLSearchParams): string {
    const fromHash = new URLSearchParams(hash.replace(/^#/, "")).get("session");
    return fromHash || search.get("session") || "";
}
