defmodule Realtime.CredentialConfineTest do
  @moduledoc """
  An API key or OAuth token socket reaches its own workspace, and there only
  what its scopes grant.
  """

  use RealtimeWeb.ChannelCase, async: false

  alias Realtime.Auth
  alias Realtime.Test.AuthRepo
  alias RealtimeWeb.BulkChannel
  alias RealtimeWeb.OrgChannel

  @org "0b7d5c1e-6a1f-4c1e-9d5a-2f0c8e4b7a10"
  @other_org "1c8e6d2f-7b2a-4d2f-8e6b-3a1d9f5c8b21"

  # Every member permission, so only the credential narrows anything.
  @all_member_permissions 8191

  @read_emails 1
  @read_campaigns 2
  @read_contacts 4
  @read_unibox 8
  @read_analytics 16
  @realtime_subscribe 2048

  defp grant(scopes, org \\ @org),
    do: Auth.credential_grant(%{organization_id: org, scopes: scopes})

  defp member(org \\ @org),
    do: %{organization_id: org, permissions: @all_member_permissions, scope: nil}

  test "each read scope reaches the member permission behind it" do
    perm = fn scopes -> grant(scopes).permissions end

    assert perm.(@realtime_subscribe) == 0
    assert perm.(@read_emails) == Auth.permission(:manage_emails)
    assert perm.(@read_campaigns) == Auth.permission(:view_campaigns)
    assert perm.(@read_contacts) == Auth.permission(:view_contacts)
    assert perm.(@read_unibox) == Auth.permission(:access_unibox)
    assert perm.(@read_analytics) == Auth.permission(:view_analytics)
  end

  test "no scope reaches team, billing or settings" do
    mask = grant(0xFFFFFFFF).permissions

    for perm <- [:manage_team, :manage_billing, :manage_settings, :transfer_ownership] do
      refute Auth.has_permission?(%{permissions: mask}, Auth.permission(perm))
    end
  end

  test "a credential keeps the member permissions its scopes grant, in its own workspace" do
    credential = grant(@read_campaigns)

    assert {:ok, %{permissions: permissions}} = Auth.confine({:ok, member()}, credential)
    assert permissions == Auth.permission(:view_campaigns)
  end

  test "a credential never reaches more than the member holds" do
    credential = grant(@read_campaigns + @read_unibox)
    viewer = %{member() | permissions: Auth.permission(:view_campaigns)}

    assert {:ok, %{permissions: permissions}} = Auth.confine({:ok, viewer}, credential)
    assert permissions == Auth.permission(:view_campaigns)
  end

  test "a credential reaches nothing in another workspace" do
    assert {:error, :not_a_member} =
             Auth.confine({:ok, member(@other_org)}, grant(@read_campaigns))
  end

  test "a credential reaches nothing outside a workspace" do
    assert {:error, :not_a_member} =
             Auth.confine({:ok, %{permissions: 65535, scope: nil}}, grant(@read_campaigns))
  end

  test "a ticket socket keeps the member's own permissions" do
    assert Auth.confine({:ok, member(@other_org)}, nil) == {:ok, member(@other_org)}
    assert Auth.confine({:error, :forbidden}, grant(@read_campaigns)) == {:error, :forbidden}
  end

  describe "org channel" do
    setup do
      start_supervised!(AuthRepo)

      AuthRepo.respond_with(fn _sql, _params ->
        {:ok, %{rows: [[uuid(), "admin", @all_member_permissions, true, true, nil]]}}
      end)

      :ok
    end

    defp credential_socket(scopes, org \\ @org),
      do: user_socket(uuid(), %{auth_type: :api_key, credential: grant(scopes, org)})

    test "a credential joins only its own workspace's channel" do
      assert {:ok, _, _} =
               subscribe_and_join(credential_socket(@read_campaigns), OrgChannel, "org:#{@org}")

      assert {:error, %{code: 4010, reason: "not_a_member"}} =
               subscribe_and_join(
                 credential_socket(@read_campaigns),
                 OrgChannel,
                 "org:#{@other_org}"
               )
    end

    test "a credential receives only the event families its scopes grant" do
      {:ok, _, socket} =
        subscribe_and_join(credential_socket(@read_campaigns), OrgChannel, "org:#{@org}")

      assert socket.assigns.permissions == Auth.permission(:view_campaigns)
      assert OrgChannel.can_see_event?(socket, %{"event_type" => "EMAIL_SENT"})
      refute OrgChannel.can_see_event?(socket, %{"event_type" => "INBOX_EMAIL_RECEIVED"})
      refute OrgChannel.can_see_event?(socket, %{"event_type" => "CONTACT_CREATED"})
      refute OrgChannel.can_see_event?(socket, %{"event_type" => "MEMBER_JOINED"})
      refute OrgChannel.can_see_event?(socket, %{"event_type" => "BILLING_UPDATED"})
    end
  end

  describe "bulk channel" do
    defp bulk_event(user_id, org) do
      %{"event_type" => "BULK_PROGRESS", "user_id" => user_id, "org_id" => org}
    end

    test "a credential receives its holder's operations in its own workspace only" do
      user_id = uuid()
      socket = user_socket(user_id, %{auth_type: :api_key, credential: grant(@read_contacts)})

      assert BulkChannel.own_event?(bulk_event(user_id, @org), socket)
      refute BulkChannel.own_event?(bulk_event(user_id, @other_org), socket)
      refute BulkChannel.own_event?(Map.delete(bulk_event(user_id, @org), "org_id"), socket)
    end

    test "a ticket socket receives its own operations wherever they ran" do
      user_id = uuid()
      assert BulkChannel.own_event?(bulk_event(user_id, @other_org), user_socket(user_id))
    end
  end
end
