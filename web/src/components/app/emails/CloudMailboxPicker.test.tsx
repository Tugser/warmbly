import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { PoolLinkWorkspaceMailbox } from "@/lib/api/models/app/cloudlink/CloudLink";
import CloudMailboxPicker from "./CloudMailboxPicker";

const fixture = vi.hoisted(() => ({
    data: [] as PoolLinkWorkspaceMailbox[], allowed: true, manageMailboxes: true, loading: false, error: false,
    adopt: vi.fn(), confirm: vi.fn(), refetch: vi.fn(),
}));
vi.mock("@/hooks/usePermission", () => ({ useInstanceAdmin: () => ({ allowed: fixture.allowed, holdsAdmin: false }), usePermission: () => fixture.manageMailboxes, showAdminMFARequired: vi.fn() }));
vi.mock("@/hooks/context/confirm", () => ({ useConfirm: () => ({ show: fixture.confirm }) }));
vi.mock("@/lib/api/hooks/app/cloudlink/useCloudLink", () => ({
    CLOUD_LINK_KEY: ["cloud-link"],
    useCloudWorkspaceMailboxes: () => ({ data: fixture.data, isLoading: fixture.loading, isError: fixture.error, error: { message: "Cloud is temporarily unavailable" }, refetch: fixture.refetch }),
}));
vi.mock("@/lib/api/client/app/cloudlink/cloudLink", () => ({ adoptCloudMailbox: fixture.adopt }));

function picker(props: Parameters<typeof CloudMailboxPicker>[0] = {}) {
    const client = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
    return render(<QueryClientProvider client={client}><CloudMailboxPicker {...props} /></QueryClientProvider>);
}

describe("existing Cloud mailbox connection", () => {
    beforeEach(() => {
        vi.clearAllMocks();
        fixture.allowed = true;
        fixture.manageMailboxes = true;
        fixture.loading = false;
        fixture.error = false;
        fixture.adopt.mockResolvedValue({});
        fixture.data = [
            { id: "google", email: "sender@workspace.test", name: "Sender", provider: "gmail", status: "active" },
            { id: "outlook", email: "sender@outlook.test", name: "Sender", provider: "outlook", status: "active" },
        ];
    });

    it("connects multiple existing mailboxes only after explaining the association", async () => {
        picker();
        fireEvent.click(screen.getByRole("button", { name: "Connect all (2)" }));
        expect(fixture.adopt).not.toHaveBeenCalled();
        expect(fixture.confirm.mock.calls[0][0]).toContain("Warmup and its history stay on Cloud");
        fixture.confirm.mock.calls[0][1]();
        await waitFor(() => expect(screen.getByText(/2 mailboxes connected/)).toBeVisible());
        expect(fixture.adopt.mock.calls.map(([id]) => id)).toEqual(["google", "outlook"]);
        expect(screen.getByText("All available mailboxes are connected.")).toBeVisible();
    });

    it("keeps an individual failure retryable while other mailboxes connect", async () => {
        fixture.adopt.mockRejectedValueOnce({ message: "Please retry this mailbox" }).mockResolvedValue({});
        picker();
        fireEvent.click(screen.getByRole("button", { name: "Connect all (2)" }));
        fixture.confirm.mock.calls[0][1]();
        await waitFor(() => expect(screen.getByText(/1 mailbox connected/)).toBeVisible());
        expect(screen.getByText(/Please retry this mailbox/)).toBeVisible();
        fireEvent.click(screen.getByRole("button", { name: "Connect sender@workspace.test" }));
        fixture.confirm.mock.calls[1][1]();
        await waitFor(() => expect(screen.getByText(/2 mailboxes connected/)).toBeVisible());
        expect(fixture.adopt.mock.calls.map(([id]) => id)).toEqual(["google", "outlook", "google"]);
    });

    it("shows the Google Cloud mailbox within the Google method chooser", () => {
        picker({ provider: "gmail", initialSearch: "workspace.test" });
        expect(screen.getByRole("button", { name: "Connect sender@workspace.test" })).toBeVisible();
        expect(screen.queryByText("sender@outlook.test")).not.toBeInTheDocument();
        fireEvent.change(screen.getByPlaceholderText("Search Cloud mailboxes"), { target: { value: "missing.test" } });
        expect(screen.getByText("No Cloud mailboxes match this search.")).toBeVisible();
    });

    it("offers a Google app-password association without changing its local sign-in", () => {
        fixture.data = [{ id: "smtp", email: "sender@workspace.test", name: "Sender", provider: "smtp_imap", status: "active", mail_host: "google_workspace" }];
        picker({ provider: "gmail" });
        fireEvent.click(screen.getByRole("button", { name: "Connect sender@workspace.test" }));
        expect(fixture.confirm.mock.calls[0][0]).toContain("existing local app-password connection is kept");
        expect(fixture.confirm.mock.calls[0][0]).not.toContain("switches to Cloud access");
    });

    it("offers a retry for a failed list instead of hiding the Cloud option", () => {
        fixture.error = true;
        fixture.data = [];
        picker();
        fireEvent.click(screen.getByRole("button", { name: "Try again" }));
        expect(fixture.refetch).toHaveBeenCalledOnce();
    });

    it("explains the administrator requirement before offering connection controls", () => {
        fixture.allowed = false;
        picker();
        expect(screen.getByText(/instance administrator signed in with two-factor/)).toBeVisible();
        expect(screen.queryByRole("button", { name: /Connect/ })).not.toBeInTheDocument();
    });

    it("requires mailbox management permission in the selected workspace", () => {
        fixture.manageMailboxes = false;
        picker();
        expect(screen.getByText(/Manage mailboxes permission in this workspace/)).toBeVisible();
        expect(screen.queryByRole("button", { name: /Connect/ })).not.toBeInTheDocument();
    });
});
