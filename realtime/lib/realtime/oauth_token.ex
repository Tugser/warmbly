defmodule Realtime.OAuthToken do
  @moduledoc """
  OAuth2 access token validation and authentication.

  Validates access tokens prefixed with `wmat_` by:
  1. Hashing with SHA-256 (same scheme as `Realtime.ApiKey`)
  2. Looking up in the `oauth_access_grants` table
  3. Checking the token is neither revoked nor expired
  4. Checking the `REALTIME_SUBSCRIBE` scope (bit 11) is granted

  Tokens carry their permissions in the `scopes` bigint, which uses the same
  api_permission bitmask as API keys.
  """

  require Logger

  alias Realtime.ApiKey
  alias Realtime.Repo

  # OAuth2 access token prefix
  @prefix "wmat_"

  # Permission bit for realtime subscription (bit 11 = value 2048)
  @perm_realtime_subscribe 11

  @doc """
  Check if a token is an OAuth2 access token (starts with `wmat_` prefix).
  """
  def is_oauth_token?(nil), do: false
  def is_oauth_token?(""), do: false

  def is_oauth_token?(token) when is_binary(token) do
    String.starts_with?(token, @prefix)
  end

  @doc """
  Validate an OAuth2 access token and return its holder and grant if valid.

  Returns:
  - {:ok, user_id, %{organization_id, scopes}} if valid
  - {:error, reason} if invalid

  Checks:
  - Token exists
  - Token is not revoked
  - Token is not expired
  - Token has realtime subscription scope (bit 11)
  """
  def validate(token, _opts \\ []) do
    with {:ok, grant} <- lookup_token(token),
         :ok <- check_revoked(grant),
         :ok <- check_expiration(grant),
         :ok <- check_scope(grant) do
      {:ok, grant.user_id, %{organization_id: grant.organization_id, scopes: grant.scopes}}
    end
  end

  # Reuse the API key hashing so both token types hash identically.
  defp hash_token(token), do: ApiKey.hash_key(token)

  # The holder must still be a member, not banned from signing in, and the app usable,
  # the same conditions the API applies when it resolves the token.
  @lookup_query """
  SELECT g.user_id::text, g.scopes, g.access_expires_at, g.revoked_at, g.organization_id::text
  FROM oauth_access_grants g
  WHERE g.access_token_hash = $1
    AND EXISTS (SELECT 1 FROM organization_members m
                WHERE m.organization_id = g.organization_id AND m.user_id = g.user_id)
    AND NOT EXISTS (SELECT 1 FROM users u WHERE u.id = g.user_id AND (u.ban_scope & 1) <> 0)
    AND EXISTS (SELECT 1 FROM oauth_applications a
                WHERE a.id = g.application_id AND a.status = 'active' AND a.suspended_at IS NULL)
  """

  defp lookup_token(token) do
    case Repo.query(@lookup_query, [hash_token(token)]) do
      {:ok, %{rows: [[user_id, scopes, expires_at, revoked_at, org_id] | _]}} ->
        {:ok,
         %{
           user_id: user_id,
           organization_id: org_id,
           scopes: scopes,
           access_expires_at: expires_at,
           revoked_at: revoked_at
         }}

      {:ok, %{rows: []}} ->
        {:error, :invalid_key}

      {:error, reason} ->
        Logger.error("OAuth token query failed: #{inspect(reason)}")
        {:error, :database_error}
    end
  end

  defp check_revoked(%{revoked_at: nil}), do: :ok

  defp check_revoked(%{revoked_at: _revoked_at}) do
    {:error, :token_revoked}
  end

  defp check_expiration(%{access_expires_at: nil}), do: :ok

  defp check_expiration(%{access_expires_at: expires_at}) when is_struct(expires_at, DateTime) do
    if DateTime.compare(DateTime.utc_now(), expires_at) == :lt do
      :ok
    else
      {:error, :key_expired}
    end
  end

  defp check_expiration(%{access_expires_at: expires_at})
       when is_struct(expires_at, NaiveDateTime) do
    if NaiveDateTime.compare(NaiveDateTime.utc_now(), expires_at) == :lt do
      :ok
    else
      {:error, :key_expired}
    end
  end

  defp check_expiration(_), do: :ok

  defp check_scope(%{scopes: scopes}) when is_integer(scopes) do
    if has_permission?(scopes, @perm_realtime_subscribe) do
      :ok
    else
      {:error, :permission_denied}
    end
  end

  defp check_scope(_) do
    # No scopes, deny by default
    {:error, :permission_denied}
  end

  defp has_permission?(scopes, bit) do
    Bitwise.band(scopes, Bitwise.bsl(1, bit)) != 0
  end
end
