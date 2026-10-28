import toast from "react-hot-toast/headless";
import type { AppError } from "@/lib/api/client/normalizeError";
import buildError from "@/lib/helper/buildError";

// reportAIError toasts a failed AI call, naming the fix when credits ran out.
export function reportAIError(e: unknown) {
    const err = e as AppError;
    toast.error(
        err?.status === 402 ? "You're out of AI credits. Upgrade or purchase more to keep writing with AI." : buildError(err),
    );
}
