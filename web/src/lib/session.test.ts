import { beforeEach, describe, expect, it } from "vitest";
import { clearClientSession } from "./session";

// setupTests.ts replaces localStorage with no-op stubs; back them with a map here.
function backLocalStorage(): Map<string, string> {
    const data = new Map<string, string>();
    const ls = window.localStorage as unknown as Record<string, unknown>;
    ls.getItem = (k: string) => data.get(k) ?? null;
    ls.setItem = (k: string, v: string) => { data.set(k, String(v)); };
    ls.removeItem = (k: string) => { data.delete(k); };
    ls.clear = () => data.clear();
    ls.key = (i: number) => [...data.keys()][i] ?? null;
    Object.defineProperty(ls, "length", { configurable: true, get: () => data.size });
    return data;
}

// Everything a signed-in person leaves in this browser is gone after sign-out,
// except the remembered-device marker and preferences that describe no workspace.
const SIGNED_IN: Record<string, string> = {
    access_token: "a", access_token_expires_at: "2099-01-01T00:00:00Z",
    refresh_token: "r", refresh_token_expires_at: "2099-01-01T00:00:00Z",
    auth_token: "{}",
    sso_binding: "b",
    "warmbly-storage": JSON.stringify({ state: { currentOrganization: { id: "org" } }, version: 1 }),
    'warmbly-reply-draft:["u","o","t","m","reply"]': "{}",
    "warmbly.sendingDomains.redirectRescue": "acme.com,acme.io",
    "warmbly.sendingDomains.setupNudge": "acme.com",
    "warmbly.compose.seedPick": '["seed-1"]',
    "remie.tips.u.o": "{}",
    "warmbly:view:u:o:contacts": "{}",
    "warmbly:signin-migration-later:o": "1",
};
const KEPT: Record<string, string> = {
    "warmbly-trusted-devices": '{"person@example.com":"device"}',
    theme: "dark",
    "warmbly:passkey-nudge-dismissed": "1",
};

describe("clearClientSession", () => {
    let local: Map<string, string>;
    beforeEach(() => {
        local = backLocalStorage();
        sessionStorage.clear();
        for (const [k, v] of Object.entries({ ...SIGNED_IN, ...KEPT })) localStorage.setItem(k, v);
        sessionStorage.setItem("sso_binding", "b");
        sessionStorage.setItem("warmbly:browse:v1:x", "{}");
        sessionStorage.setItem("warmbly:mailbox-tag:x", "t");
    });

    it("leaves only the remembered-device marker and workspace-free preferences", () => {
        clearClientSession();
        expect([...local.keys()].sort()).toEqual(Object.keys(KEPT).sort());
        expect(Object.keys(sessionStorage)).toEqual([]);
    });
});
