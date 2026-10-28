import { API_BASE_URL } from "@/lib/information";
import getToken from "@/lib/helper/getToken";
import type { AgentStreamEvent } from "@/lib/api/models/app/agent/Agent";
import { readEventStream } from "../../eventStream";

// streamAgentRun POSTs to an SSE agent endpoint and invokes onEvent for each
// `data:` frame. Uses raw fetch (not the axios client) because the response is
// a streamed body. Aborting the passed signal cancels the run server-side (the
// run executes in the request context), which is the panel's stop button.
export default async function streamAgentRun(
    path: string,
    body: Record<string, unknown>,
    onEvent: (ev: AgentStreamEvent) => void,
    signal?: AbortSignal,
): Promise<void> {
    const token = getToken();
    let res: Response;
    try {
        res = await fetch(`${API_BASE_URL}${path}`, {
            method: "POST",
            headers: {
                "Content-Type": "application/json",
                Accept: "text/event-stream",
                ...(token?.access_token
                    ? { Authorization: `Bearer ${token.access_token}` }
                    : {}),
            },
            body: JSON.stringify(body),
            signal,
        });
    } catch (e) {
        if ((e as Error)?.name === "AbortError") return;
        onEvent({ type: "error", message: "Could not reach Remie." });
        return;
    }

    // Non-SSE error (auth, not found, service unavailable) comes back as JSON.
    if (!res.ok || !res.body) {
        try {
            const j = await res.json();
            onEvent({
                type: "error",
                code: j.code,
                message: j.message || "Remie is unavailable.",
            });
        } catch {
            onEvent({ type: "error", message: "Remie is unavailable." });
        }
        return;
    }

    try {
        await readEventStream(res.body, (data) => onEvent(data as AgentStreamEvent));
    } catch (e) {
        if ((e as Error)?.name !== "AbortError") {
            onEvent({ type: "error", message: "The connection was interrupted." });
        }
    }
}
