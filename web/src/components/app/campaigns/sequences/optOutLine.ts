import type Campaign from "@/lib/api/models/app/campaigns/Campaign";
import { DEFAULT_UNSUBSCRIBE, type UnsubscribeSettings } from "@/lib/api/models/app/outreach/OutreachSettings";

// The opt-out line a send appends, resolved the way the send path does
// (models.UnsubscribeSettings.Effective): the campaign's mode over the
// workspace's, blank copy filled with the defaults. null when it is off.
export function effectiveOptOut(
    workspace: UnsubscribeSettings | undefined,
    campaignMode: Campaign["unsubscribe_mode"] | undefined,
): UnsubscribeSettings | null {
    const base = workspace ?? DEFAULT_UNSUBSCRIBE;
    const mode = campaignMode && campaignMode !== "inherit" ? campaignMode : base.mode || "text";
    if (mode === "off") return null;
    return {
        mode,
        text: base.text.trim() || DEFAULT_UNSUBSCRIBE.text,
        link_intro: base.link_intro.trim() || DEFAULT_UNSUBSCRIBE.link_intro,
        link_text: base.link_text.trim() || DEFAULT_UNSUBSCRIBE.link_text,
    };
}
