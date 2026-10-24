import React from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { CheckIcon, CloudIcon, Loader2Icon } from "lucide-react";
import { useCloudWorkspaceMailboxes, CLOUD_LINK_KEY } from "@/lib/api/hooks/app/cloudlink/useCloudLink";
import { adoptCloudMailbox } from "@/lib/api/client/app/cloudlink/cloudLink";
import type { PoolLinkWorkspaceMailbox } from "@/lib/api/models/app/cloudlink/CloudLink";
import type { AppError } from "@/lib/api/client/normalizeError";
import buildError from "@/lib/helper/buildError";
import { useConfirm } from "@/hooks/context/confirm";
import { useInstanceAdmin, usePermission, showAdminMFARequired } from "@/hooks/usePermission";
import { SearchInput } from "@/components/ui/field";
import ProviderLogo from "./ProviderLogo";
import { sourceMutationKey } from "@/lib/api/hooks/app/emails/mailboxSourceBusy";

export default function CloudMailboxPicker({ provider, initialSearch = "" }: { provider?: "gmail" | "outlook"; initialSearch?: string }) {
    const admin = useInstanceAdmin();
    const manageMailboxes = usePermission("MANAGE_EMAILS");
    const list = useCloudWorkspaceMailboxes(admin.allowed);
    const qc = useQueryClient();
    const confirm = useConfirm();
    const [search, setSearch] = React.useState(initialSearch);
    const [connected, setConnected] = React.useState<Set<string>>(new Set());
    const [failed, setFailed] = React.useState<Record<string, { mailbox: PoolLinkWorkspaceMailbox; message: string }>>({});
    const [progress, setProgress] = React.useState({ done: 0, total: 0, current: "" });
    React.useEffect(() => setSearch(initialSearch), [initialSearch]);

    const connect = useMutation({
        mutationKey: sourceMutationKey("cloud-mailbox-connect"),
        mutationFn: async (mailboxes: PoolLinkWorkspaceMailbox[]) => {
            setProgress({ done: 0, total: mailboxes.length, current: "" });
            for (const [index, mailbox] of mailboxes.entries()) {
                setProgress({ done: index, total: mailboxes.length, current: mailbox.email });
                try {
                    await adoptCloudMailbox(mailbox.id);
                    setConnected((before) => new Set([...before, mailbox.id]));
                    setFailed((before) => {
                        const next = { ...before };
                        delete next[mailbox.id];
                        return next;
                    });
                } catch (error) {
                    setFailed((before) => ({ ...before, [mailbox.id]: { mailbox, message: buildError(error as AppError) } }));
                }
                setProgress({ done: index + 1, total: mailboxes.length, current: "" });
            }
        },
        onSettled: () => {
            void qc.invalidateQueries({ queryKey: CLOUD_LINK_KEY });
            void qc.invalidateQueries({ queryKey: ["emails"] });
        },
    });

    if (!admin.allowed) {
        return <div className="px-4 py-3 text-[11.5px] text-slate-500">
            Existing Cloud mailboxes can be connected by an instance administrator signed in with two-factor authentication.
            {admin.holdsAdmin && <button type="button" onClick={showAdminMFARequired} className="ml-1 text-sky-700 underline">Verify your identity</button>}
        </div>;
    }
    if (!manageMailboxes) return <p className="px-4 py-3 text-[11.5px] text-slate-500">You need the Manage mailboxes permission in this workspace to connect Cloud mailboxes.</p>;
    const available = new Map((list.data ?? []).map((mailbox) => [mailbox.id, mailbox]));
    for (const { mailbox } of Object.values(failed)) available.set(mailbox.id, mailbox);
    const items = [...available.values()].filter((mailbox) => {
        if (connected.has(mailbox.id)) return false;
        if (!provider || mailbox.provider === provider) return true;
        return mailbox.provider === "smtp_imap" && (provider === "gmail" ? ["gmail", "google_workspace"].includes(mailbox.mail_host ?? "") : ["outlook", "microsoft365"].includes(mailbox.mail_host ?? ""));
    });
    const filtered = items.filter((mailbox) => `${mailbox.email} ${mailbox.name}`.toLowerCase().includes(search.toLowerCase().trim()));
    const busy = connect.isPending;
    if (!list.isLoading && !list.isError && items.length === 0 && connected.size === 0 && !initialSearch) return null;

    const askConnect = (mailboxes: PoolLinkWorkspaceMailbox[]) => {
        const subject = mailboxes.length === 1 ? mailboxes[0].email : `these ${mailboxes.length} mailboxes`;
        const smtpOnly = mailboxes.every((mailbox) => mailbox.provider === "smtp_imap");
        const credential = smtpOnly ? "The existing local app-password connection is kept. Each side keeps its own credentials; the passwords do not have to match." : "A matching local app-password mailbox switches to Cloud access for a Google or Microsoft sign-in, keeping its campaigns and messages. Cloud app-password mailboxes keep their existing local connection.";
        confirm.show(`Connect ${subject} from your linked Cloud workspace? Warmup and its history stay on Cloud; campaigns send from this instance. ${credential}`, () => { connect.mutate(mailboxes); });
    };

    return <section aria-label="In your Warmbly Cloud workspace" className="px-4 py-3 bg-sky-50/40 space-y-2">
        <div className="flex items-center gap-1.5">
            <CloudIcon className="w-3.5 h-3.5 text-sky-600" />
            <span className="text-[10px] uppercase tracking-[0.14em] text-slate-500 font-medium">In your Warmbly Cloud workspace</span>
        </div>
        <p className="text-[11.5px] text-slate-600">Already connected on Cloud? Use it here for campaigns. Keep the same Cloud mailbox and all its warmup history, without recreating accounts or restarting warmup.</p>
        {initialSearch && <p className="text-[11.5px] text-sky-800">Connect the existing Cloud mailbox below to associate it with this instance.</p>}
        {list.isLoading && <p role="status" className="text-[12px] text-slate-500">Loading your Cloud mailboxes…</p>}
        {list.isError && <div role="alert" className="text-[12px] text-amber-800">
            <p>Your Cloud mailboxes could not be loaded. {buildError(list.error as unknown as AppError)}</p>
            <button type="button" onClick={() => void list.refetch()} className="mt-1 text-sky-700 underline">Try again</button>
        </div>}
        {connected.size > 0 && <p role="status" className="text-[12px] text-emerald-700">{connected.size} {connected.size === 1 ? "mailbox connected" : "mailboxes connected"}. Cloud warmup history is preserved.</p>}
        {(items.length > 3 || search) && <SearchInput value={search} onChange={setSearch} placeholder="Search Cloud mailboxes" />}
        {filtered.length > 1 && <button type="button" disabled={busy} onClick={() => askConnect(filtered)} className="h-7 px-2.5 rounded-md bg-slate-900 text-white text-[12px] font-medium disabled:opacity-50">
            Connect {search ? "matching" : "all"} ({filtered.length})
        </button>}
        {busy && <p role="status" className="flex items-center gap-1.5 text-[12px] text-slate-600"><Loader2Icon className="size-3 animate-spin" />Connecting {progress.done + (progress.current ? 1 : 0)} of {progress.total}{progress.current ? `: ${progress.current}` : ""}</p>}
        <div className="max-h-64 overflow-y-auto space-y-1.5">
            {filtered.map((mailbox) => <div key={mailbox.id} className="rounded-md border border-slate-200 bg-white px-2.5 py-2">
                <div className="flex items-center gap-2.5">
                    <ProviderLogo id={mailbox.mail_host || mailbox.provider} size="sm" />
                    <div className="min-w-0 flex-1"><p className="text-[12.5px] text-slate-900 truncate">{mailbox.email}</p><p className="text-[11px] text-slate-500">{mailbox.provider === "smtp_imap" ? "App password · uses your existing local connection" : "Warmup stays on Cloud. Campaigns send here."}</p></div>
                    <button type="button" aria-label={`Connect ${mailbox.email}`} disabled={busy} onClick={() => askConnect([mailbox])} className="shrink-0 h-7 px-2.5 rounded-md bg-slate-900 text-white text-[12px] font-medium inline-flex items-center gap-1 disabled:opacity-50"><CheckIcon className="size-3" />{failed[mailbox.id] ? "Retry" : "Connect"}</button>
                </div>
                {failed[mailbox.id] && <p role="alert" className="mt-1 text-[11.5px] text-amber-800">{failed[mailbox.id].message}</p>}
            </div>)}
        </div>
        {!list.isLoading && !list.isError && filtered.length === 0 && <p className="text-[12px] text-slate-500">{items.length > 0 ? "No Cloud mailboxes match this search." : connected.size > 0 ? "All available mailboxes are connected." : "No available Cloud mailbox matches this address. Check the selected Cloud workspace and refresh."}</p>}
    </section>;
}
