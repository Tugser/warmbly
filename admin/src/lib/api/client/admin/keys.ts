// /admin/keys/status: where every value the instance keys seal stands, and
// whether every fleet node runs the env the backend renders now. Read only.

import { Request } from "@/lib/api/client";

export interface KeyColumnCount {
    table: string;
    column: string;
    domain: "instance" | "mfa";
    primary: number;
    read_key: number;
    workspace: number;
    unsealed: number;
    unreadable: number;
    missing?: boolean;
}

export interface KeyGraceWindow {
    name: string;
    until: string;
    open: boolean;
    entries: number;
}

export interface KeyReport {
    applied: boolean;
    instance_read_keys: number;
    mfa_read_keys: number;
    mfa_configured: boolean;
    columns: KeyColumnCount[] | null;
    grace_windows: KeyGraceWindow[] | null;
    checked_at: string;
}

export interface NodeEnvState {
    id: string;
    name: string;
    role: string;
    applied?: string;
    tracked: boolean;
    current: boolean;
}

export interface KeyStatus {
    keys: KeyReport;
    fleet?: { all_current: boolean; nodes: NodeEnvState[] | null };
    ready: { retire_read_keys: boolean; retire_tokens: boolean };
}

export function getKeyStatus(refresh = false): Promise<KeyStatus> {
    return Request({
        method: "GET",
        url: refresh ? "/admin/keys/status?refresh=true" : "/admin/keys/status",
        authorization: true,
    });
}
