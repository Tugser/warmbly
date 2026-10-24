import { renderHook } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import useMailboxOAuth from "./useMailboxOAuth";

const fixture = vi.hoisted(() => ({ connected: true }));
vi.mock("@/hooks/useCloudPool", () => ({ default: () => ({ connected: fixture.connected, workspaceConnected: false }) }));
vi.mock("@/hooks/useGmailOAuthConnect", () => ({ default: (viaCloud: boolean) => viaCloud }));

describe("existing Cloud connections in Add account", () => {
    it("keeps a connected legacy link usable without disconnecting it", () => {
        fixture.connected = true;
        const client = new QueryClient();
        const hook = renderHook(useMailboxOAuth, { wrapper: ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider> });
        expect(hook.result.current.viaCloud).toBe(true);
        expect(hook.result.current.gmailAvailable).toBe(true);
        fixture.connected = false;
        hook.rerender();
        expect(hook.result.current.viaCloud).toBe(false);
    });
});
