// Configuration, keys: whether a key rotation can be finished. Shows which
// sealed values still need a read key, which credential windows are open and
// which nodes have not picked up the current env. Read only: rotation runs
// where the keys are stored, never from the browser.

import { useQueryClient, useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { ExternalLink, KeyRound, RefreshCw, ShieldAlert, ShieldCheck } from "lucide-react";
import { ErrorState } from "@/components/ErrorState";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Callout, EmptyState, StatusBadge } from "@/components/ui/kit";
import { SettingsGroup, SettingsRow } from "./SettingsLayout";
import { getKeyStatus, type KeyColumnCount, type KeyStatus } from "@/lib/api/client/admin/keys";
import { docsUrl } from "@/lib/docs";
import { TONE_TEXT } from "@/lib/tones";
import { cn } from "@/lib/utils";

const QUERY_KEY = ["admin", "keys", "status"] as const;

export function KeysTab() {
    const queryClient = useQueryClient();
    const [refreshing, setRefreshing] = useState(false);
    const statusQ = useQuery({
        queryKey: QUERY_KEY,
        queryFn: () => getKeyStatus(),
        retry: false,
    });

    async function refresh() {
        setRefreshing(true);
        try {
            queryClient.setQueryData(QUERY_KEY, await getKeyStatus(true));
        } catch {
            await statusQ.refetch();
        } finally {
            setRefreshing(false);
        }
    }

    return (
        <div>
            <div className="mb-8 flex flex-wrap items-start justify-between gap-3">
                <p className="max-w-2xl text-[13px] leading-relaxed text-muted-foreground">
                    Whether a key rotation can be finished. A rotation keeps the old key as a read
                    key while stored values are resealed; it is safe to retire once nothing below
                    still needs it. Rotation itself runs where the keys are stored.
                </p>
                <div className="flex items-center gap-1.5">
                    <Button size="sm" variant="outline" asChild>
                        <a href={docsUrl("/development/configuration/#rotate-instance-keys")} target="_blank" rel="noreferrer">
                            <ExternalLink />
                            Rotation guide
                        </a>
                    </Button>
                    <Button size="sm" variant="outline" onClick={() => void refresh()} disabled={refreshing || statusQ.isLoading}>
                        <RefreshCw className={refreshing ? "animate-spin" : undefined} />
                        Check again
                    </Button>
                </div>
            </div>

            {statusQ.isLoading && (
                <div className="space-y-10">
                    {[0, 1].map((i) => (
                        <div key={i}>
                            <Skeleton className="mb-3 h-4 w-32" />
                            <Skeleton className="h-40 w-full rounded-lg" />
                        </div>
                    ))}
                </div>
            )}

            {statusQ.isError && (
                <ErrorState
                    error={statusQ.error}
                    title="Could not check the instance keys"
                    onRetry={() => statusQ.refetch()}
                />
            )}

            {statusQ.data && <KeyStatusView status={statusQ.data} />}
        </div>
    );
}

function KeyStatusView({ status }: { status: KeyStatus }) {
    const { keys, fleet, ready } = status;
    const columns = (keys.columns ?? []).filter((c) => !c.missing);
    const onReadKey = columns.reduce((n, c) => n + c.read_key, 0);
    const unreadable = columns.reduce((n, c) => n + c.unreadable, 0);
    const windows = keys.grace_windows ?? [];
    const nodes = fleet?.nodes ?? [];
    const readKeys = keys.instance_read_keys + keys.mfa_read_keys;

    return (
        <>
            {unreadable > 0 ? (
                <Callout tone="danger" icon={ShieldAlert} title={`${unreadable} stored ${unreadable === 1 ? "value opens" : "values open"} under no configured key`}>
                    Retire nothing. Restore the key that sealed them as a read key first.
                </Callout>
            ) : onReadKey > 0 ? (
                <Callout tone="warning" icon={KeyRound} title={`${onReadKey} ${onReadKey === 1 ? "value still needs" : "values still need"} a read key`}>
                    Run the reseal step of the rotation, then check again.
                </Callout>
            ) : (
                <Callout tone="success" icon={ShieldCheck} title="Every sealed value opens under the primary key">
                    {readKeys > 0
                        ? "The read keys can be retired once no rollback target or retained backup still needs them."
                        : "No read keys are configured, so there is nothing to retire."}
                </Callout>
            )}

            <SettingsGroup title="Keys" description={`Checked ${new Date(keys.checked_at).toLocaleString()}.`}>
                <SettingsRow label="Instance credential key" description="Seals mailbox credentials, webhook secrets and integration tokens.">
                    <StatusBadge tone={keys.instance_read_keys > 0 ? "warning" : "neutral"}>
                        {readKeyLabel(keys.instance_read_keys)}
                    </StatusBadge>
                </SettingsRow>
                <SettingsRow label="MFA key" description="Seals TOTP enrollments.">
                    {keys.mfa_configured ? (
                        <StatusBadge tone={keys.mfa_read_keys > 0 ? "warning" : "neutral"}>{readKeyLabel(keys.mfa_read_keys)}</StatusBadge>
                    ) : (
                        <StatusBadge>Not configured</StatusBadge>
                    )}
                </SettingsRow>
                <SettingsRow label="Read keys retirable" description="Every value opens under the primary and none is unreadable.">
                    <StatusBadge tone={ready.retire_read_keys ? "success" : "neutral"} dot>
                        {ready.retire_read_keys ? "Yes" : readKeys > 0 ? "Not yet" : "Nothing to retire"}
                    </StatusBadge>
                </SettingsRow>
            </SettingsGroup>

            <SettingsGroup title="Sealed values" description="Per column: values under the primary, under a read key, under their workspace key, stored unsealed, and unreadable.">
                {columns.length === 0 ? (
                    <EmptyState icon={KeyRound} title="No sealed columns on this instance." />
                ) : (
                    <div className="overflow-x-auto">
                        <table className="w-full text-[12.5px]">
                            <thead>
                                <tr className="text-left text-[10px] uppercase tracking-[0.14em] text-muted-foreground">
                                    <th className="px-4 py-2 font-medium">Column</th>
                                    <th className="px-3 py-2 text-right font-medium">Primary</th>
                                    <th className="px-3 py-2 text-right font-medium">Read key</th>
                                    <th className="px-3 py-2 text-right font-medium">Workspace</th>
                                    <th className="px-3 py-2 text-right font-medium">Unsealed</th>
                                    <th className="px-4 py-2 text-right font-medium">Unreadable</th>
                                </tr>
                            </thead>
                            <tbody className="divide-y divide-border">
                                {columns.map((c) => (
                                    <ColumnRow key={`${c.table}.${c.column}`} c={c} />
                                ))}
                            </tbody>
                        </table>
                    </div>
                )}
            </SettingsGroup>

            {windows.length > 0 && (
                <SettingsGroup title="Credential windows" description="Previous tokens and signing secrets still accepted while senders move to the new value.">
                    {windows.map((w) => (
                        <SettingsRow
                            key={w.name}
                            label={<span className="font-mono text-[12px]">{w.name}</span>}
                            description={`${w.entries} previous ${w.entries === 1 ? "value" : "values"}`}
                        >
                            <StatusBadge tone={w.open ? "info" : "neutral"}>
                                {w.open ? `Open until ${new Date(w.until).toLocaleString()}` : "Closed"}
                            </StatusBadge>
                        </SettingsRow>
                    ))}
                </SettingsGroup>
            )}

            {fleet && (
                <SettingsGroup
                    title="Fleet env"
                    description="A token can be retired once every live node runs the env the backend renders now. Nodes marked manual do not refresh their env on their own and need a rejoin."
                >
                    {nodes.length === 0 ? (
                        <EmptyState title="No live nodes." />
                    ) : (
                        nodes.map((n) => (
                            <SettingsRow key={n.id} label={n.name || n.id} description={n.role}>
                                <StatusBadge tone={n.current ? "success" : n.tracked ? "warning" : "neutral"} dot>
                                    {n.current ? "Current" : n.tracked ? "Updating" : "Manual"}
                                </StatusBadge>
                            </SettingsRow>
                        ))
                    )}
                </SettingsGroup>
            )}
        </>
    );
}

function ColumnRow({ c }: { c: KeyColumnCount }) {
    return (
        <tr>
            <td className="px-4 py-2 font-mono text-[12px] text-foreground">
                {c.table}.{c.column}
                {c.domain === "mfa" && <span className="ml-1.5 text-muted-foreground">(MFA)</span>}
            </td>
            <Cell n={c.primary} />
            <Cell n={c.read_key} tone="warning" />
            <Cell n={c.workspace} />
            <Cell n={c.unsealed} />
            <Cell n={c.unreadable} tone="danger" last />
        </tr>
    );
}

function Cell({ n, tone, last }: { n: number; tone?: "warning" | "danger"; last?: boolean }) {
    const color = n === 0 ? TONE_TEXT.neutral : TONE_TEXT[tone ?? "strong"];
    return <td className={cn(last ? "px-4" : "px-3", "py-2 text-right tabular-nums", color)}>{n.toLocaleString()}</td>;
}

function readKeyLabel(n: number) {
    return n === 0 ? "Primary only" : `Primary + ${n} read ${n === 1 ? "key" : "keys"}`;
}
