// Shared pieces of the floating AI cards: their surface, and the one-tap
// changes offered while reviewing what AI wrote.

import { BriefcaseIcon, MinusIcon, PlusIcon, SmileIcon, SpellCheckIcon } from "lucide-react";
import { AI_PILL_CLASS } from "./AIBusyStatus";
import type { EditorAIPhase } from "./useEditorAIRun";

export interface AIRefinement {
    label: string;
    icon: React.ReactNode;
    instruction: string;
}

export const AI_CARD_CLASS =
    "rounded-lg border border-slate-200 bg-white shadow-[0_12px_32px_-8px_rgba(15,23,42,0.22)] overflow-hidden";

// The surface for a run's phase: a pill while it works, a card to review.
export function aiRunCardClass(phase: EditorAIPhase): string {
    return phase === "review" ? AI_CARD_CLASS : AI_PILL_CLASS;
}

const icon = "w-3.5 h-3.5 text-slate-400";

export const WRITE_REFINEMENTS: AIRefinement[] = [
    { label: "Make it shorter", icon: <MinusIcon className={icon} />, instruction: "Make this more concise. Cut filler and keep the meaning." },
    { label: "Make it longer", icon: <PlusIcon className={icon} />, instruction: "Expand this slightly with more substance and specificity. No fluff." },
    { label: "More casual", icon: <SmileIcon className={icon} />, instruction: "Make the tone more casual and human, like a quick note to a peer." },
];

export const EDIT_REFINEMENTS: AIRefinement[] = [
    { label: "Make it shorter", icon: <MinusIcon className={icon} />, instruction: "Make this more concise. Cut filler and keep the meaning." },
    { label: "More formal", icon: <BriefcaseIcon className={icon} />, instruction: "Make the tone more professional and polished." },
    { label: "Fix grammar", icon: <SpellCheckIcon className={icon} />, instruction: "Fix spelling, grammar, and punctuation only. Change nothing else." },
];
