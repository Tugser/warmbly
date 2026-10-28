defmodule RealtimeWeb.CrossWorkspaceJoinTest do
  @moduledoc """
  A signed-in member's socket joins only its own workspace's and its own user's topics.
  """

  use RealtimeWeb.ChannelCase, async: false

  alias Realtime.Test.AuthRepo
  alias RealtimeWeb.OrgChannel
  alias RealtimeWeb.UserChannel

  @org "0b7d5c1e-6a1f-4c1e-9d5a-2f0c8e4b7a10"
  @other_org "1c8e6d2f-7b2a-4d2f-8e6b-3a1d9f5c8b21"

  setup do
    start_supervised!(AuthRepo)
    me = uuid()

    # The membership query answers a row only for (my org, me); anything else is no row.
    AuthRepo.respond_with(fn
      _sql, [org_bin, user_bin | _] ->
        if org_bin == Ecto.UUID.dump!(@org) and user_bin == Ecto.UUID.dump!(me) do
          {:ok, %{rows: [[uuid(), "admin", 8191, true, true, nil]]}}
        else
          {:ok, %{rows: []}}
        end

      _sql, _params ->
        {:ok, %{rows: []}}
    end)

    %{me: me}
  end

  test "a member joins its own workspace's channel", %{me: me} do
    assert {:ok, _, _} = subscribe_and_join(user_socket(me), OrgChannel, "org:#{@org}")
  end

  test "a member cannot join another workspace's channel", %{me: me} do
    assert {:error, %{reason: "not_a_member"}} =
             subscribe_and_join(user_socket(me), OrgChannel, "org:#{@other_org}")
  end

  test "a member cannot join another user's channel", %{me: me} do
    assert {:error, %{reason: "unauthorized"}} =
             subscribe_and_join(user_socket(me), UserChannel, "user:#{uuid()}")
  end
end
