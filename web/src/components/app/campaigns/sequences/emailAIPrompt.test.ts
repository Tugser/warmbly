import { describe, expect, it } from "vitest";
import { SUBJECT_MOVES, draftPrompt, parseDraft, parseSubjects, stepLabel, subjectsPrompt } from "./emailAIPrompt";

describe("draftPrompt", () => {
    it("asks for a first email with a subject when the step opens the conversation", () => {
        const p = draftPrompt({ brief: "Offer a free ads audit", ctx: { earlier: [], threaded: false }, wantSubject: true, currentBody: "" });
        expect(p).toContain("first cold email");
        expect(p).toContain("Offer a free ads audit");
        expect(p).toContain('"Subject: <subject>"');
        expect(p).not.toContain("follow-up");
    });

    it("hands a follow-up the emails already sent and asks for the body only in a thread", () => {
        const p = draftPrompt({
            brief: "",
            ctx: { earlier: [{ subject: "ads for {{.Company}}", body: "Hi {{.FirstName}}, quick idea." }], threaded: true },
            wantSubject: false,
            currentBody: "",
        });
        expect(stepLabel({ earlier: [{ subject: "", body: "" }], threaded: true })).toBe("Follow-up 1");
        expect(p).toContain("follow-up 1");
        expect(p).toContain("reply in the same thread");
        expect(p).toContain("--- Email 1 (subject: ads for {{.Company}})\nHi {{.FirstName}}, quick idea.");
        expect(p).toContain("Return only the body");
    });

    it("rewrites the current draft when there is no brief", () => {
        const p = draftPrompt({ brief: "", ctx: { earlier: [], threaded: false }, wantSubject: false, currentBody: "We sell widgets." });
        expect(p).toContain("Rewrite this draft");
        expect(p).toContain("We sell widgets.");
    });
});

describe("parseDraft", () => {
    it("splits the subject line off the body", () => {
        expect(parseDraft("Subject: quick idea for {{.Company}}\n\nHi {{.FirstName}},\n\nOne thing.")).toEqual({
            subject: "quick idea for {{.Company}}",
            body: "Hi {{.FirstName}},\n\nOne thing.",
        });
    });

    it("tolerates markdown, quotes and a code fence", () => {
        expect(parseDraft('```\n**Subject:** "your q3 hiring"\n\nBody:\nHi there\n```')).toEqual({
            subject: "your q3 hiring",
            body: "Hi there",
        });
    });

    it("keeps the whole text as the body when there is no subject line", () => {
        expect(parseDraft("Hi {{.FirstName}},\n\nNo subject here.")).toEqual({ subject: null, body: "Hi {{.FirstName}},\n\nNo subject here." });
    });
});

describe("parseSubjects", () => {
    it("drops numbering, bullets, quotes and repeats", () => {
        expect(parseSubjects('1. "slow invoices?"\n- Subject: slow invoices?\n2) your q3 pipeline\n\n• {{.Company}} + payroll')).toEqual([
            "slow invoices?",
            "your q3 pipeline",
            "{{.Company}} + payroll",
        ]);
    });

    it("mentions earlier subjects for a follow-up", () => {
        expect(subjectsPrompt({ ctx: { earlier: [{ subject: "first one", body: "" }], threaded: false }, body: "x", brief: "" })).toContain(
            "Earlier subjects: first one",
        );
    });

    it("rewrites the current subject toward a move and offers the workspace's fields", () => {
        const p = subjectsPrompt({
            ctx: { earlier: [], threaded: false },
            body: "",
            brief: "",
            current: "our new payroll tool",
            goal: SUBJECT_MOVES[0].goal,
            fields: ["industry"],
        });
        expect(p).toContain("Rewrite this cold email subject line 5 different ways: our new payroll tool");
        expect(p).toContain("Make it shorter");
        expect(p).toContain("{{.industry}}");
        expect(p).toContain('{{.Company | default "your team"}}');
    });
});
