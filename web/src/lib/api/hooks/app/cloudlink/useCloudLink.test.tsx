import { act, renderHook } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import { useEnrollCloudLinkMailbox } from "./useCloudLink";
import { CLOUD_MAILBOX_PICKER_EVENT, cloudMailboxPickerOpened } from "@/lib/cloudMailboxConnect";

const fixture = vi.hoisted(() => ({ enroll: vi.fn(), allowed: true }));
vi.mock("@/hooks/usePermission", () => ({ useInstanceAdmin: () => ({ allowed: fixture.allowed }) }));
vi.mock("@/lib/api/hooks/app/organizations/useCurrentOrganization", () => ({ default: () => ({ data: { id: "workspace" } }) }));
vi.mock("@/lib/api/client/app/cloudlink/cloudLink", async (original) => ({ ...await original<object>(), enrollCloudLinkMailbox: fixture.enroll }));

describe("Cloud enrollment recovery", () => {
    beforeEach(() => { vi.clearAllMocks(); fixture.allowed = true; });

    it("opens the matching Cloud mailbox picker without adopting by address alone", async () => {
        fixture.enroll.mockRejectedValue({ code: "pool_link_mailbox_conflict", status: 409 });
        const client = new QueryClient();
        client.setQueryData(["cloud-link", "workspace", "mailboxes"], [{ id: "local", email: "sender@workspace.test" }]);
        const opened = vi.fn();
        window.addEventListener(CLOUD_MAILBOX_PICKER_EVENT, opened);
        const hook = renderHook(useEnrollCloudLinkMailbox, { wrapper: ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider> });
        try {
            let failure: unknown;
            await act(async () => { try { await hook.result.current.mutateAsync("local"); } catch (error) { failure = error; } });
            expect(cloudMailboxPickerOpened(failure)).toBe(true);
            expect(opened).toHaveBeenCalledOnce();
            expect((opened.mock.calls[0][0] as CustomEvent).detail.email).toBe("sender@workspace.test");
        } finally {
            window.removeEventListener(CLOUD_MAILBOX_PICKER_EVENT, opened);
        }
    });

    it("explains the required administrator action to workspace members", async () => {
        fixture.allowed = false;
        fixture.enroll.mockRejectedValue({ code: "pool_link_mailbox_conflict", status: 409 });
        const client = new QueryClient();
        const hook = renderHook(useEnrollCloudLinkMailbox, { wrapper: ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider> });
        await act(async () => {
            await expect(hook.result.current.mutateAsync("local")).rejects.toMatchObject({ message: expect.stringContaining("instance administrator signed in with two-factor") });
        });
    });
});
