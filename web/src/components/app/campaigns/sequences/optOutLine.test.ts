import { describe, expect, it } from "vitest";
import { DEFAULT_UNSUBSCRIBE } from "@/lib/api/models/app/outreach/OutreachSettings";
import { effectiveOptOut } from "./optOutLine";

describe("effectiveOptOut", () => {
    const workspace = { mode: "link" as const, text: "", link_intro: "Not for you?", link_text: "" };

    it("follows the workspace when the campaign inherits, filling blank copy", () => {
        expect(effectiveOptOut(workspace, "inherit")).toEqual({
            mode: "link",
            text: DEFAULT_UNSUBSCRIBE.text,
            link_intro: "Not for you?",
            link_text: DEFAULT_UNSUBSCRIBE.link_text,
        });
    });

    it("lets the campaign override the mode, and off adds nothing", () => {
        expect(effectiveOptOut(workspace, "text")?.mode).toBe("text");
        expect(effectiveOptOut(workspace, "off")).toBeNull();
        expect(effectiveOptOut({ ...workspace, mode: "off" }, "inherit")).toBeNull();
    });

    it("uses the reply-to-opt-out sentence before settings load", () => {
        expect(effectiveOptOut(undefined, undefined)?.text).toBe(DEFAULT_UNSUBSCRIBE.text);
    });
});
