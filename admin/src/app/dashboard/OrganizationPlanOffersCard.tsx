// Private plans this workspace may buy. A private plan is in no public list
// and the checkout refuses it without an open offer, so this card is the only
// way to sell one (the Partnership plan, a partner price).
//
// Reading needs view_organizations; offering and withdrawing need
// manage_organizations.

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { CalendarClock, Tag, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { StatusBadge } from "@/components/ui/kit";
import { ErrorState } from "@/components/ErrorState";
import { useConfirm } from "@/components/ConfirmDialog";
import { useAdminPerm } from "@/hooks/useAdminPerm";
import { AdminPerm } from "@/lib/auth/permissions";
import {
    grantOrganizationPlanOffer,
    listAdminPlans,
    listOrganizationPlanOffers,
    withdrawOrganizationPlanOffer,
} from "@/lib/api/client/admin/organizations";
import { endOfLocalDay, fmtDay } from "./grantDates";

// The Warmup plan has its own checkout, so it is never offered.
const WARMUP_PLAN_ID = "00000000-0000-0000-0000-000000000002";

export function OrganizationPlanOffersCard({ orgId }: { orgId: string }) {
    const qc = useQueryClient();
    const confirm = useConfirm();
    const canManage = useAdminPerm(AdminPerm.ManageOrganizations);
    const [offering, setOffering] = useState(false);
    const [planId, setPlanId] = useState("");
    const [reason, setReason] = useState("");
    const [until, setUntil] = useState("");
    const key = ["admin", "organizations", orgId, "plan-offers"];

    const offersQuery = useQuery({
        queryKey: key,
        queryFn: () => listOrganizationPlanOffers(orgId),
        enabled: !!orgId,
    });
    const plansQuery = useQuery({
        queryKey: ["admin", "plans"],
        queryFn: () => listAdminPlans(),
        enabled: offering,
    });

    const offerMutation = useMutation({
        mutationFn: () =>
            grantOrganizationPlanOffer(orgId, planId, {
                reason: reason.trim(),
                expires_at: until ? endOfLocalDay(until) : null,
            }),
        onSuccess: () => {
            qc.invalidateQueries({ queryKey: key });
            toast.success("Plan offered");
            setOffering(false);
            setPlanId("");
            setReason("");
            setUntil("");
        },
        onError: (e: Error) => toast.error(e.message || "Failed to offer the plan"),
    });

    const withdrawMutation = useMutation({
        mutationFn: (id: string) => withdrawOrganizationPlanOffer(orgId, id),
        onSuccess: () => {
            qc.invalidateQueries({ queryKey: key });
            toast.success("Offer withdrawn");
        },
        onError: (e: Error) => toast.error(e.message || "Failed to withdraw the offer"),
    });

    async function withdraw(id: string, name: string) {
        const ok = await confirm({
            title: `Withdraw the ${name} offer?`,
            description:
                "The workspace can no longer start a checkout for this plan. A subscription it already bought is not touched.",
            confirmLabel: "Withdraw",
            destructive: true,
        });
        if (ok) await withdrawMutation.mutateAsync(id).catch(() => undefined);
    }

    if (offersQuery.isLoading) return <Skeleton className="h-24 w-full rounded-lg" />;
    if (offersQuery.error || !offersQuery.data) {
        return (
            <ErrorState
                error={offersQuery.error}
                title="Failed to load plan offers."
                onRetry={() => offersQuery.refetch()}
            />
        );
    }

    const offers = offersQuery.data.offers ?? [];
    const open = offers.filter((o) => o.active).length;
    const offered = new Set(offers.filter((o) => o.active).map((o) => o.plan_id));
    const plans = (plansQuery.data?.plans ?? []).filter((p) => p.public === false && p.id !== WARMUP_PLAN_ID);

    return (
        <div className="overflow-hidden surface-lit rounded-xl border border-border bg-card">
            <div className="flex flex-wrap items-start justify-between gap-3 px-4 py-3">
                <div className="min-w-0">
                    <div className="flex items-center gap-2">
                        <Tag className="size-4 text-subtle-foreground" />
                        <span className="text-[13px] font-medium text-foreground">Plan offers</span>
                        {open > 0 && (
                            <StatusBadge tone="info" dot>
                                {open} open
                            </StatusBadge>
                        )}
                    </div>
                    <p className="mt-1 text-[12.5px] text-muted-foreground">
                        {offers.length === 0
                            ? "No private plan offered. The workspace can buy public plans and the Warmup plan only."
                            : "Private plans this workspace can buy from its billing page."}
                    </p>
                </div>
                {canManage && (
                    <Button size="sm" variant="outline" onClick={() => setOffering((v) => !v)}>
                        <Tag className="size-3.5" /> Offer a plan
                    </Button>
                )}
            </div>

            {offers.length > 0 && (
                <ul className="divide-y divide-border border-t border-border">
                    {offers.map((o) => {
                        const name = o.plan?.name || o.plan_id;
                        return (
                            <li key={o.plan_id} className="flex flex-wrap items-center gap-3 px-4 py-2.5">
                                <div className="min-w-0 flex-1">
                                    <div className="flex flex-wrap items-center gap-2">
                                        <span className="text-[13px] font-medium text-foreground">{name}</span>
                                        {o.plan?.price != null && (
                                            <span className="text-xs tabular-nums text-muted-foreground">
                                                ${o.plan.price}/mo
                                            </span>
                                        )}
                                        <StatusBadge tone={o.active ? "success" : "warning"} dot>
                                            {o.active ? "open" : "lapsed"}
                                        </StatusBadge>
                                    </div>
                                    <p className="mt-0.5 break-words text-xs text-muted-foreground">
                                        {o.reason} · offered {fmtDay(o.granted_at)}
                                        {o.expires_at ? (
                                            <span className="inline-flex items-center gap-1">
                                                {" "}
                                                · <CalendarClock className="size-3" /> until {fmtDay(o.expires_at)}
                                            </span>
                                        ) : (
                                            " · open-ended"
                                        )}
                                    </p>
                                </div>
                                {canManage && (
                                    <Button
                                        size="sm"
                                        variant="ghost"
                                        onClick={() => withdraw(o.plan_id, name)}
                                        disabled={withdrawMutation.isPending}
                                    >
                                        <X className="size-3.5" /> Withdraw
                                    </Button>
                                )}
                            </li>
                        );
                    })}
                </ul>
            )}

            {offering && canManage && (
                <div className="space-y-3 border-t border-border bg-muted/30 px-4 py-4">
                    <div className="grid gap-3 sm:grid-cols-2">
                        <div className="space-y-1.5">
                            <Label htmlFor="plan-offer-plan" className="text-xs font-medium text-muted-foreground">
                                Private plan
                            </Label>
                            <Select value={planId} onValueChange={setPlanId}>
                                <SelectTrigger id="plan-offer-plan">
                                    <SelectValue
                                        placeholder={
                                            plansQuery.isLoading
                                                ? "Loading plans…"
                                                : plans.length === 0
                                                  ? "No private plans"
                                                  : "Choose a plan…"
                                        }
                                    />
                                </SelectTrigger>
                                <SelectContent>
                                    {plans.map((p) => (
                                        <SelectItem key={p.id} value={p.id}>
                                            {p.name}
                                            {p.price != null ? ` ($${p.price}/mo)` : ""}
                                            {offered.has(p.id) ? " (offered)" : ""}
                                        </SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        </div>
                        <div className="space-y-1.5">
                            <Label htmlFor="plan-offer-until" className="text-xs font-medium text-muted-foreground">
                                Offer expires (optional)
                            </Label>
                            <Input
                                id="plan-offer-until"
                                type="date"
                                value={until}
                                onChange={(e) => setUntil(e.target.value)}
                            />
                        </div>
                    </div>
                    <div className="space-y-1.5">
                        <Label htmlFor="plan-offer-reason" className="text-xs font-medium text-muted-foreground">
                            Reason
                        </Label>
                        <Input
                            id="plan-offer-reason"
                            placeholder="Why this workspace may buy this plan"
                            value={reason}
                            maxLength={500}
                            onChange={(e) => setReason(e.target.value)}
                        />
                    </div>
                    <p className="text-xs leading-relaxed text-muted-foreground">
                        An offer lets the workspace check out; it does not put it on the plan. Offering a plan again
                        renews the offer with the new reason and expiry.
                    </p>
                    <div className="flex items-center gap-1.5">
                        <Button
                            size="sm"
                            onClick={() => offerMutation.mutate()}
                            disabled={!planId || !reason.trim() || offerMutation.isPending}
                        >
                            Offer
                        </Button>
                        <Button size="sm" variant="ghost" onClick={() => setOffering(false)}>
                            Cancel
                        </Button>
                    </div>
                </div>
            )}
        </div>
    );
}
