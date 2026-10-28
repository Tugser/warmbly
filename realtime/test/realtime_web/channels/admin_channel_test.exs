defmodule RealtimeWeb.AdminChannelTest do
  use RealtimeWeb.ChannelCase, async: false

  alias RealtimeWeb.AdminChannel
  alias Realtime.Test.AuthRepo

  setup do
    start_supervised!(AuthRepo)
    AuthRepo.respond_with(fn _sql, _params -> {:ok, %{rows: [[1]]}} end)
    :ok
  end

  test "each distinct event is delivered once and leaving removes the subscription" do
    Process.flag(:trap_exit, true)
    {:ok, _, socket} = subscribe_and_join(admin_socket(), AdminChannel, "admin:platform")
    :sys.get_state(socket.channel_pid)

    assert Enum.count(Registry.lookup(Realtime.PubSub, socket.topic), fn {pid, _} ->
             pid == socket.channel_pid
           end) == 1

    for n <- [1, 2] do
      event = %{"event_type" => "TASK_PROGRESS", "processed_items" => n}
      Realtime.EventBroadcaster.broadcast(event)
      assert_push("TASK_PROGRESS", ^event)
      refute_push("TASK_PROGRESS", _payload, 50)
    end

    leave(socket)
    ref = Process.monitor(socket.channel_pid)
    assert_receive {:DOWN, ^ref, :process, _, _}
    Realtime.EventBroadcaster.broadcast(%{"event_type" => "TASK_PROGRESS"})
    refute_push("TASK_PROGRESS", _payload, 50)
  end

  test "non-admin and non-JWT sockets cannot join" do
    AuthRepo.respond_with(fn _sql, _params -> {:ok, %{rows: []}} end)

    assert {:error, %{reason: "not_an_admin"}} =
             subscribe_and_join(admin_socket(), AdminChannel, "admin:platform")

    assert {:error, %{reason: "jwt_required"}} =
             subscribe_and_join(
               user_socket(uuid(), %{auth_type: :api_key, mfa_verified: true}),
               AdminChannel,
               "admin:platform"
             )
  end

  test "an admin whose session did not verify a second factor cannot join" do
    assert {:error, %{reason: "admin_mfa_required"}} =
             subscribe_and_join(user_socket(uuid()), AdminChannel, "admin:platform")

    assert {:error, %{reason: "admin_mfa_required"}} =
             subscribe_and_join(
               user_socket(uuid(), %{mfa_verified: false}),
               AdminChannel,
               "admin:platform"
             )

    assert AuthRepo.queries() == []
  end

  defp admin_socket, do: user_socket(uuid(), %{mfa_verified: true})
end
