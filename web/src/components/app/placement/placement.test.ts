import { describe, expect, it } from "vitest";
import type { PlacementDay } from "@/lib/api/models/app/analytics/WarmupPlacement";
import { observedCount, rescueSummary, totals, viewDays } from "./placement";

const legacy: PlacementDay = {
    date: "2026-10-01", inbox: 1, tabs: 0, spam: 1, rescued: 0,
    sent: 10, delivered: 2, unconfirmed: 4, inbox_rate: 50, spam_rate: 50,
    rolling_inbox_rate: null, groups: [],
};

describe("placement observation scope", () => {
    it("preserves missing evidence from older APIs", () => {
        const day = viewDays([legacy], "all", 20, 7)[0];
        expect(day.unknown).toBeUndefined();
        expect(day.archived).toBeUndefined();
        expect(day.custom).toBeUndefined();
        expect(totals([day]).inboxRate).toBe(50);
    });

    it("keeps unclassified receipts visible but outside rate denominators", () => {
        const day = viewDays([{ ...legacy, unknown: 2, archived: 1, custom: 1 }], "all", 20, 7)[0];
        expect(observedCount(day)).toBe(6);
        expect(day.delivered).toBe(2);
        expect(totals([day]).inboxRate).toBe(50);
    });

    it("does not invent inbox placement for an unknown-only provider", () => {
        const day = viewDays([{ ...legacy, groups: [{ group: "google", inbox: 0, tabs: 0, spam: 0, rescued: 0, unknown: 2, archived: 1, custom: 1 }] }], "google", 1, 7)[0];
        expect(observedCount(day)).toBe(4);
        expect(day.rolling).toBeNull();
        expect(totals([day]).inboxRate).toBeNull();
        expect(day.sent).toBe(0);
    });
});

describe("spam rescue outcomes", () => {
    it("reports what the recipients' mailboxes said each rescue came to", () => {
        const day = { ...legacy, spam: 7, rescued: 7, rescue_confirmed: 4, rescue_missing: 1, rescue_failed: 1 };
        const t = totals(viewDays([day], "all", 1, 7));
        expect(t.rescueConfirmed).toBe(4);
        expect(rescueSummary(t)).toBe("of 7 requested · 1 no longer there · 1 refused · 1 no outcome reported");
    });

    it("treats an older API's missing outcomes as unreported", () => {
        const t = totals(viewDays([{ ...legacy, rescued: 2 }], "all", 1, 7));
        expect(t.rescueConfirmed).toBe(0);
        expect(rescueSummary(t)).toBe("of 2 requested · 2 no outcome reported");
    });
});
