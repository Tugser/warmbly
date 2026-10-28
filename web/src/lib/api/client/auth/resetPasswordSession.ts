import Request from "../Request";

export interface ResetPasswordSession {
    session: string;
    expires_at: string;
}

// Exchanges the emailed link token, sent in the body, for the reset session the confirm step takes.
export default async function resetPasswordSession(token: string): Promise<ResetPasswordSession> {
    return await Request<ResetPasswordSession>({
        method: "POST",
        url: "/auth/reset-password/session",
        data: { token },
    })
}
