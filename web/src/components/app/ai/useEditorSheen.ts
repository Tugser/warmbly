import React from "react";

// useEditorSheen sweeps the blue AI sheen over an editor while AI writes into it.
export default function useEditorSheen(dom: HTMLElement | null | undefined, on: boolean) {
    React.useEffect(() => {
        if (!on || !dom) return;
        dom.classList.add("ai-writing");
        return () => dom.classList.remove("ai-writing");
    }, [dom, on]);
}
