// Mailboxes held from sending. An unknown hold waits for an operator to check
// the provider and say whether the send went out; the others wait for the
// owner to reconnect or for an operator's provider confirmation. Holds publish
// no admin event, so this polls at 60s.

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { CheckCircle2, KeyRound, PauseCircle, PowerOff, ShieldAlert } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Callout, Section, Stat, StatGrid, StatusBadge } from "@/components/ui/kit";
import { DataTable, type Column } from "@/components/data/DataTable";
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
} from "@/components/ui/dialog";
import { useMe } from "@/hooks/useMe";
import { AdminPerm } from "@/lib/auth/permissions";
import { canMonitor } from "@/lib/monitoring";
import {
    listSendHolds,
    resolveSendHold,
    type AdminResolveSendHoldInput,
    type AdminSendHold,
    type AdminSendHoldReason,
} from "@/lib/api/client/admin/sends";
import { absolute, relative, shortId } from "@/app/dashboard/jobs/format";
import { TabIntro, WorkspaceLink } from "@/app/dashboard/sends/shared";
import { StatusSegments } from "@/app/dashboard/sends/StatusSegments";
import { fmt } from "@/app/dashboard/sends/status";
import type { Tone } from "@/lib/tones";

const REASON_LABEL: Record<string, string> = {
    unknown: "Outcome unknown",
    conflict: "Conflicting outcomes",
    permanent: "Permanent rejection",
    authentication: "Sign-in refused",
};

const REASON_TONE: Record<string, Tone> = {
    unknown: "warning",
    conflict: "warning",
    permanent: "danger",
    authentication: "danger",
};

// Why the resolve action is not offered, in the operator's terms.
const BLOCKER_TEXT: Record<string, string> = {
    mailbox_inactive: "Switched off. The owner reconnecting the mailbox clears a sign-in hold; anything else can be resolved once it is back on.",
    task_missing: "No send is recorded against this hold, so there is nothing to confirm.",
    resolution_pending: "A verdict is recorded and the result consumer is applying it.",
    result_settling: "The worker's result is being applied; the hold lifts on its own.",
    task_not_settled: "The send is still running. Wait for the worker to answer.",
    too_recent: "The send finished moments ago. Give the worker's answer a couple of minutes.",
    other_unknown_sends: "Sends with an unknown outcome are queued behind this hold; they surface here once it lifts.",
    provider_changed: "The mailbox moved to another provider since the hold was set.",
};

type Filter = "active" | "inactive" | "all";

export function HoldsTab() {
    const { data: me } = useMe();
    const canResolve = canMonitor(me?.admin_permissions, AdminPerm.StopCampaigns);
    const [filter, setFilter] = useState<Filter>("active");
    const [resolving, setResolving] = useState<AdminSendHold | null>(null);

    const { data, isLoading, error, refetch } = useQuery({
        queryKey: ["admin", "sends", "holds"],
        queryFn: () => listSendHolds(500),
        refetchInterval: 60_000,
    });

    const summary = data?.summary;
    const all = data?.data ?? [];
    const rows = filter === "all" ? all : all.filter((r) => (r.mailbox_status === "active") === (filter === "active"));

    const columns: Column<AdminSendHold>[] = [
        {
            id: "mailbox",
            header: "Mailbox",
            cell: (r) => (
                <div className="min-w-0">
                    <div className="truncate text-[13px] text-foreground">{r.mailbox_email}</div>
                    <div className="text-[11px] text-subtle-foreground">
                        {r.provider} · {r.mailbox_status === "active" ? "on" : r.mailbox_status}
                    </div>
                </div>
            ),
            csv: (r) => r.mailbox_email,
        },
        {
            id: "workspace",
            header: "Workspace",
            cell: (r) => <WorkspaceLink id={r.organization_id} name={r.organization_name} />,
            csv: (r) => r.organization_name || "",
        },
        {
            id: "reason",
            header: "Held for",
            cell: (r) => (
                <StatusBadge tone={REASON_TONE[r.reason] ?? "neutral"} dot>
                    {REASON_LABEL[r.reason] ?? r.reason}
                </StatusBadge>
            ),
            csv: (r) => r.reason,
        },
        {
            id: "send",
            header: "Held send",
            className: "max-w-xs py-2",
            cell: (r) => (
                <div className="min-w-0 space-y-0.5 text-xs">
                    <div className="font-mono text-foreground">
                        {r.task_type || "none"} <span className="text-subtle-foreground">{shortId(r.held_task_id)}</span>
                    </div>
                    {r.recipients.length > 0 && <div className="truncate text-muted-foreground" title={r.recipients.join(", ")}>to {r.recipients.join(", ")}</div>}
                    {r.message_id && <div className="truncate font-mono text-[11px] text-subtle-foreground" title={r.message_id}>{r.message_id}</div>}
                </div>
            ),
            csv: (r) => r.held_task_id || "",
        },
        {
            id: "since",
            header: "Since",
            cell: (r) => (
                <span className="whitespace-nowrap text-xs text-muted-foreground tabular-nums" title={absolute(r.held_since)}>
                    {relative(r.held_since, "unknown")}
                </span>
            ),
            csv: (r) => r.held_since || "",
        },
        {
            id: "action",
            header: "",
            align: "right",
            className: "max-w-xs py-2",
            cell: (r) =>
                r.resolvable ? (
                    canResolve ? (
                        <Button
                            size="xs"
                            variant="outline"
                            onClick={(e) => {
                                e.stopPropagation();
                                setResolving(r);
                            }}
                        >
                            <CheckCircle2 /> Resolve
                        </Button>
                    ) : (
                        <span className="text-xs text-subtle-foreground">Needs the stop-campaigns permission</span>
                    )
                ) : (
                    <span className="block text-left text-xs leading-snug text-muted-foreground">
                        {BLOCKER_TEXT[r.blocker ?? ""] ?? "Not resolvable right now."}
                    </span>
                ),
        },
    ];

    return (
        <div>
            <TabIntro
                actions={
                    <StatusSegments
                        value={filter}
                        onChange={setFilter}
                        options={[
                            { value: "active", label: "Switched on", count: summary?.active },
                            { value: "inactive", label: "Switched off", count: summary?.inactive },
                            { value: "all", label: "All", count: summary?.total },
                        ]}
                    />
                }
            >
                A held mailbox sends nothing until the hold lifts, so a send is never repeated on a guess. A sign-in hold lifts when the owner reconnects. An unknown outcome needs someone to check the mailbox's Sent folder and record what happened.
            </TabIntro>

            <StatGrid className="md:grid-cols-4">
                <Stat icon={ShieldAlert} label="Switched on and held" value={fmt(summary?.active)} loading={isLoading} tone={summary?.active ? "warning" : undefined} />
                <Stat icon={PauseCircle} label="Outcome unknown" value={fmt(summary?.by_reason.unknown ?? (summary ? 0 : undefined))} loading={isLoading} />
                <Stat icon={KeyRound} label="Sign-in refused" value={fmt(summary?.by_reason.authentication ?? (summary ? 0 : undefined))} loading={isLoading} />
                <Stat icon={PowerOff} label="Switched off" value={fmt(summary?.inactive)} loading={isLoading} sub="waiting on the owner" />
            </StatGrid>

            <Section title="Held mailboxes">
                <DataTable
                    columns={columns}
                    rows={rows}
                    getRowId={(r) => r.email_account_id}
                    loading={isLoading}
                    error={error}
                    onRetry={() => refetch()}
                    errorTitle="Failed to load send holds"
                    storageKey="admin.sends.holds"
                    csvName="warmbly-send-holds"
                    noun="mailboxes"
                    emptyTitle="No held mailboxes"
                    emptyHint={filter === "inactive" ? "No switched-off mailbox carries a send hold." : "Every switched-on mailbox may send. A send in progress is not listed."}
                />
            </Section>

            {resolving && <ResolveHoldDialog hold={resolving} onClose={() => setResolving(null)} />}
        </div>
    );
}

function ResolveHoldDialog({ hold, onClose }: { hold: AdminSendHold; onClose: () => void }) {
    const qc = useQueryClient();
    const unknown = hold.reason === "unknown";
    const [outcome, setOutcome] = useState<"sent" | "not_sent">("sent");
    const [messageId, setMessageId] = useState(hold.message_id);
    const [reference, setReference] = useState("");

    const mutation = useMutation({
        mutationFn: (input: AdminResolveSendHoldInput) => resolveSendHold(hold.email_account_id, input),
        onSuccess: (res) => {
            toast.success(res.applied_by_consumer ? "Recorded. The hold lifts once the result consumer applies it, usually within a minute." : "Hold lifted. The mailbox can send again.");
            qc.invalidateQueries({ queryKey: ["admin", "sends", "holds"] });
            qc.invalidateQueries({ queryKey: ["admin", "instance", "monitoring"] });
            onClose();
        },
        onError: (err: Error) => toast.error(err.message || "Failed to resolve the hold"),
    });

    const needsMessageId = unknown && outcome === "sent";
    const ready = reference.trim() !== "" && (!needsMessageId || messageId.trim() !== "") && !!hold.held_task_id;

    function submit() {
        if (!ready || !hold.held_task_id) return;
        mutation.mutate({
            held_task_id: hold.held_task_id,
            held_reason: hold.reason as AdminSendHoldReason,
            outcome: unknown ? outcome : "provider_confirmed",
            message_id: needsMessageId ? messageId.trim() : undefined,
            confirmation_reference: reference.trim(),
        });
    }

    return (
        <Dialog open onOpenChange={(open) => !open && !mutation.isPending && onClose()}>
            <DialogContent>
                <DialogHeader>
                    <DialogTitle>{unknown ? "Record what happened to this send" : "Lift this send hold"}</DialogTitle>
                    <DialogDescription>
                        <span className="font-medium text-foreground">{hold.mailbox_email}</span>
                        {hold.organization_name ? ` in ${hold.organization_name}` : ""}.{" "}
                        {unknown
                            ? "Check the mailbox's Sent folder at the provider first. Not finding a copy is not proof it was not sent: some providers keep no copy of SMTP sends."
                            : "Only lift it once the provider confirms the cause is fixed. If it is not, the next send is refused and the hold comes back."}
                    </DialogDescription>
                </DialogHeader>

                {hold.recipients.length > 0 && (
                    <p className="text-xs text-muted-foreground">
                        Look for mail to <span className="text-foreground">{hold.recipients.join(", ")}</span>
                        {hold.reserved_at ? ` around ${absolute(hold.reserved_at)}` : ""}.
                    </p>
                )}

                <div className="space-y-3">
                    {unknown && (
                        <div className="space-y-1.5">
                            <Label className="text-xs font-medium text-muted-foreground">What the provider shows</Label>
                            <StatusSegments
                                value={outcome}
                                onChange={setOutcome}
                                options={[
                                    { value: "sent", label: "It went out" },
                                    { value: "not_sent", label: "It did not go out" },
                                ]}
                            />
                        </div>
                    )}
                    {needsMessageId && (
                        <div className="space-y-1.5">
                            <Label htmlFor="hold-message-id" className="text-xs font-medium text-muted-foreground">Message-ID of the sent copy</Label>
                            <Input id="hold-message-id" value={messageId} onChange={(e) => setMessageId(e.target.value)} placeholder="<abc123@mail.example.com>" maxLength={998} />
                        </div>
                    )}
                    <div className="space-y-1.5">
                        <Label htmlFor="hold-reference" className="text-xs font-medium text-muted-foreground">Where you checked</Label>
                        <Input
                            id="hold-reference"
                            value={reference}
                            onChange={(e) => setReference(e.target.value)}
                            placeholder={unknown ? "Gmail Sent folder, checked by Anna" : "Provider support ticket 48213"}
                            maxLength={256}
                        />
                    </div>
                    {unknown && outcome === "not_sent" && (
                        <Callout tone="warning">It is recorded as a failed attempt: a campaign step is sent again, and a one-off email is reported to its sender as failed. Be sure it is not in Sent.</Callout>
                    )}
                </div>

                <DialogFooter>
                    <Button variant="outline" onClick={onClose} disabled={mutation.isPending}>
                        Cancel
                    </Button>
                    <Button onClick={submit} disabled={!ready || mutation.isPending}>
                        {mutation.isPending ? "Recording…" : "Record and lift"}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}
