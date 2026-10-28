// Credential-free Cloud warmup: the workspace's linked instances may enroll
// mailboxes by address alone and run their warmup themselves, while Cloud
// checks what they report. Buying a plan does not imply it; an operator
// grants it here. The mailbox table shows how far Cloud trusts each
// instance-run mailbox's placement reports.

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { KeyRound, ShieldCheck, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import { StatusBadge } from "@/components/ui/kit";
import { ErrorState } from "@/components/ErrorState";
import { useConfirm } from "@/components/ConfirmDialog";
import { useAdminPerm } from "@/hooks/useAdminPerm";
import { AdminPerm } from "@/lib/auth/permissions";
import type { Tone } from "@/lib/tones";
import {
    getOrganizationSelfExecution,
    grantOrganizationSelfExecution,
    listOrganizationSelfExecutedMailboxes,
    revokeOrganizationSelfExecution,
} from "@/lib/api/client/admin/organizations";
import type { ReporterTrustState, SelfExecutionStatus } from "@/lib/api/models/admin";
import { endOfLocalDay, fmtDay } from "./grantDates";

const TRUST_TONE: Record<ReporterTrustState, Tone> = {
    trusted: "success",
    probation: "warning",
    untrusted: "danger",
};

function relative(ts?: string | null): string {
    if (!ts) return "never";
    const mins = Math.round((Date.now() - new Date(ts).getTime()) / 60000);
    if (mins < 1) return "just now";
    if (mins < 60) return `${mins}m ago`;
    const hours = Math.round(mins / 60);
    if (hours < 48) return `${hours}h ago`;
    return fmtDay(ts);
}

export function OrganizationSelfExecutionCard({ orgId }: { orgId: string }) {
    const qc = useQueryClient();
    const confirm = useConfirm();
    const canManage = useAdminPerm(AdminPerm.ManageOrganizations);
    const [granting, setGranting] = useState(false);
    const [reason, setReason] = useState("");
    const [until, setUntil] = useState("");
    const key = ["admin", "organizations", orgId, "self-execution"];

    const grantQuery = useQuery({
        queryKey: key,
        queryFn: () => getOrganizationSelfExecution(orgId),
        enabled: !!orgId,
    });
    const mailboxesQuery = useQuery({
        queryKey: [...key, "mailboxes"],
        queryFn: () => listOrganizationSelfExecutedMailboxes(orgId),
        enabled: !!orgId,
    });

    const grantMutation = useMutation({
        mutationFn: () =>
            grantOrganizationSelfExecution(orgId, {
                reason: reason.trim(),
                expires_at: until ? endOfLocalDay(until) : null,
            }),
        onSuccess: (status: SelfExecutionStatus) => {
            qc.setQueryData(key, status);
            toast.success("Credential-free Cloud warmup granted");
            setGranting(false);
            setReason("");
            setUntil("");
        },
        onError: (e: Error) => toast.error(e.message || "Failed to grant"),
    });

    const revokeMutation = useMutation({
        mutationFn: () => revokeOrganizationSelfExecution(orgId),
        onSuccess: () => {
            qc.setQueryData(key, { grant: null, active: false } satisfies SelfExecutionStatus);
            qc.invalidateQueries({ queryKey: [...key, "mailboxes"] });
            toast.success("Credential-free Cloud warmup revoked");
        },
        onError: (e: Error) => toast.error(e.message || "Failed to revoke"),
    });

    async function revoke() {
        const ok = await confirm({
            title: "Revoke credential-free Cloud warmup?",
            description:
                "Linked instances can no longer enroll mailboxes without credentials, and warmup pauses for the mailboxes they already run until the permission is granted again.",
            confirmLabel: "Revoke",
            destructive: true,
        });
        if (ok) await revokeMutation.mutateAsync().catch(() => undefined);
    }

    if (grantQuery.isLoading) return <Skeleton className="h-24 w-full rounded-lg" />;
    if (grantQuery.error || !grantQuery.data) {
        return (
            <ErrorState
                error={grantQuery.error}
                title="Failed to load the credential-free warmup permission."
                onRetry={() => grantQuery.refetch()}
            />
        );
    }

    const { grant, active } = grantQuery.data;
    const lapsed = !!grant && !active;
    const mailboxes = mailboxesQuery.data;

    return (
        <div className="overflow-hidden surface-lit rounded-xl border border-border bg-card">
            <div className="flex flex-wrap items-start justify-between gap-3 px-4 py-3">
                <div className="min-w-0">
                    <div className="flex items-center gap-2">
                        <KeyRound className="size-4 text-subtle-foreground" />
                        <span className="text-[13px] font-medium text-foreground">Credential-free Cloud warmup</span>
                        {active ? (
                            <StatusBadge tone="info" dot>
                                granted
                            </StatusBadge>
                        ) : lapsed ? (
                            <StatusBadge tone="warning" dot>
                                lapsed
                            </StatusBadge>
                        ) : null}
                    </div>
                    <p className="mt-1 text-[12.5px] text-muted-foreground">
                        {active
                            ? "Linked instances may enroll mailboxes by address and keep every credential themselves."
                            : lapsed
                              ? "The permission has lapsed; mailboxes run by linked instances are paused."
                              : "Not granted. Linked instances must hand Cloud a credential for each mailbox they enroll."}
                    </p>
                </div>
                {canManage && (
                    <div className="flex shrink-0 items-center gap-1.5">
                        {grant && (
                            <Button size="sm" variant="outline" onClick={revoke} disabled={revokeMutation.isPending}>
                                <X className="size-3.5" /> Revoke
                            </Button>
                        )}
                        <Button size="sm" variant="outline" onClick={() => setGranting((v) => !v)}>
                            <ShieldCheck className="size-3.5" />
                            {grant ? "Renew" : "Grant"}
                        </Button>
                    </div>
                )}
            </div>

            {grant && (
                <dl className="grid grid-cols-1 gap-px border-t border-border bg-border sm:grid-cols-3">
                    <div className="bg-card px-4 py-2.5">
                        <dt className="text-xs text-muted-foreground">Reason</dt>
                        <dd className="mt-0.5 break-words text-[13px] text-foreground">{grant.reason}</dd>
                    </div>
                    <div className="bg-card px-4 py-2.5">
                        <dt className="text-xs text-muted-foreground">Granted</dt>
                        <dd className="mt-0.5 text-[13px] tabular-nums text-foreground">{fmtDay(grant.granted_at)}</dd>
                    </div>
                    <div className="bg-card px-4 py-2.5">
                        <dt className="text-xs text-muted-foreground">Expires</dt>
                        <dd className="mt-0.5 text-[13px] tabular-nums text-foreground">
                            {grant.expires_at ? fmtDay(grant.expires_at) : "open-ended"}
                        </dd>
                    </div>
                </dl>
            )}

            {granting && canManage && (
                <div className="space-y-3 border-t border-border bg-muted/30 px-4 py-4">
                    <div className="grid gap-3 sm:grid-cols-2">
                        <div className="space-y-1.5">
                            <Label htmlFor="self-exec-reason" className="text-xs font-medium text-muted-foreground">
                                Reason
                            </Label>
                            <Input
                                id="self-exec-reason"
                                placeholder="Why this workspace may keep its credentials"
                                value={reason}
                                maxLength={500}
                                onChange={(e) => setReason(e.target.value)}
                            />
                        </div>
                        <div className="space-y-1.5">
                            <Label htmlFor="self-exec-until" className="text-xs font-medium text-muted-foreground">
                                Expires (optional)
                            </Label>
                            <Input
                                id="self-exec-until"
                                type="date"
                                value={until}
                                onChange={(e) => setUntil(e.target.value)}
                            />
                        </div>
                    </div>
                    <p className="text-xs leading-relaxed text-muted-foreground">
                        Cloud still checks every report: arrivals need a token minted for that mailbox, sends count only
                        once the recipient proves them, and placement reports that disagree with what Cloud sees itself
                        stop counting.
                    </p>
                    <div className="flex items-center gap-1.5">
                        <Button
                            size="sm"
                            onClick={() => grantMutation.mutate()}
                            disabled={!reason.trim() || grantMutation.isPending}
                        >
                            {grant ? "Renew" : "Grant"}
                        </Button>
                        <Button size="sm" variant="ghost" onClick={() => setGranting(false)}>
                            Cancel
                        </Button>
                    </div>
                </div>
            )}

            {mailboxesQuery.isLoading ? (
                <div className="border-t border-border px-4 py-3">
                    <Skeleton className="h-10 w-full rounded-md" />
                </div>
            ) : mailboxesQuery.error ? (
                <div className="border-t border-border px-4 py-3 text-xs text-muted-foreground">
                    Could not load reporter trust.{" "}
                    <button type="button" className="underline" onClick={() => mailboxesQuery.refetch()}>
                        Retry
                    </button>
                </div>
            ) : mailboxes && mailboxes.length > 0 ? (
                <div className="overflow-x-auto border-t border-border">
                    <table className="w-full text-left text-[12.5px]">
                        <thead className="text-xs text-muted-foreground">
                            <tr className="border-b border-border">
                                <th className="px-4 py-2 font-medium">Mailbox</th>
                                <th className="px-4 py-2 font-medium">Reports</th>
                                <th className="px-4 py-2 font-medium tabular-nums">Samples</th>
                                <th className="px-4 py-2 font-medium tabular-nums">Divergence</th>
                                <th className="px-4 py-2 font-medium">Last lease</th>
                            </tr>
                        </thead>
                        <tbody className="divide-y divide-border">
                            {mailboxes.map((mb) => (
                                <tr key={mb.email_account_id}>
                                    <td className="max-w-[16rem] truncate px-4 py-2 text-foreground">{mb.email}</td>
                                    <td className="px-4 py-2">
                                        <StatusBadge tone={TRUST_TONE[mb.trust_state] ?? "neutral"} dot>
                                            {mb.trust_state}
                                        </StatusBadge>
                                    </td>
                                    <td className="px-4 py-2 tabular-nums text-muted-foreground">
                                        {mb.reported_samples}
                                    </td>
                                    <td className="px-4 py-2 tabular-nums text-muted-foreground">
                                        {mb.divergence == null ? "—" : `${Math.round(mb.divergence * 100)} pts`}
                                    </td>
                                    <td className="px-4 py-2 text-muted-foreground">{relative(mb.last_leased_at)}</td>
                                </tr>
                            ))}
                        </tbody>
                    </table>
                </div>
            ) : mailboxes ? (
                <p className="border-t border-border px-4 py-3 text-xs text-muted-foreground">
                    No mailbox is run by a linked instance yet.
                </p>
            ) : null}
        </div>
    );
}
