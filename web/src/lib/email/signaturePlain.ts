// Renders an HTML signature down to its plain-text alternative: block elements
// and <br> become line breaks, stylesheets and scripts are dropped.
export function signatureToPlain(html: string): string {
    const withBreaks = html
        .replace(/<\s*br\s*\/?>/gi, "\n")
        .replace(/<\/\s*(p|div|h[1-6]|li|tr)\s*>/gi, "\n");
    if (typeof DOMParser === "undefined") return withBreaks.replace(/<[^>]+>/g, "");
    // DOMParser builds an inert document: nothing is fetched and no handler
    // runs. Assigning innerHTML on a detached div does fire <img onerror>.
    const doc = new DOMParser().parseFromString(withBreaks, "text/html");
    // Without this a signature carrying a <style> block put its whole
    // stylesheet into the plain-text alternative.
    doc.querySelectorAll("style, script, title, noscript, template").forEach((el) => el.remove());
    return (doc.body.textContent || "").replace(/[ \t]+/g, " ").replace(/\n{3,}/g, "\n\n").trim();
}
