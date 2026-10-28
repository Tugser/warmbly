import Request from "../Request";
import type Token from "../../models/auth/Token";

// Every session ends with the change, this one included; the response is the
// token pair of a new session for this device and must replace the stored one.
export default async function changePassword(data: {
    current_password: string;
    new_password: string;
    // Also ends the user's app authorizations and the API keys they created.
    revoke_api_credentials?: boolean;
}): Promise<Token> {
    return await Request<Token>({
        method: "POST",
        url: "/auth/me/password",
        authorization: true,
        data,
    })
}
