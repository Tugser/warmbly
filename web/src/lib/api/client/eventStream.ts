// readEventStream reads a text/event-stream body and hands the parsed JSON of
// each frame's `data:` line to onData. Frames that are not JSON are skipped.
export async function readEventStream(body: ReadableStream<Uint8Array>, onData: (data: unknown) => void): Promise<void> {
    const reader = body.getReader();
    const decoder = new TextDecoder();
    let buffer = "";
    for (;;) {
        const { done, value } = await reader.read();
        if (done) break;
        buffer += decoder.decode(value, { stream: true });
        let sep: number;
        // SSE frames are separated by a blank line.
        while ((sep = buffer.indexOf("\n\n")) >= 0) {
            const frame = buffer.slice(0, sep);
            buffer = buffer.slice(sep + 2);
            const dataLine = frame.split("\n").find((l) => l.startsWith("data:"));
            const json = dataLine?.slice(5).trim();
            if (!json) continue;
            try {
                onData(JSON.parse(json));
            } catch {
                /* skip malformed frame */
            }
        }
    }
}
