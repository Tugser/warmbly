defmodule Realtime.WsTicketClaimOutageTest do
  @moduledoc """
  A ticket that cannot be marked used opens no socket.
  """

  use ExUnit.Case, async: false

  alias Realtime.Auth
  alias Realtime.Test.AuthRepo
  alias Realtime.Test.Counter

  setup do
    Counter.reset()
    start_supervised!(AuthRepo)
    on_exit(fn -> Counter.simulate_outage(false) end)
    :ok
  end

  test "a ticket is refused while its single use cannot be recorded" do
    AuthRepo.respond_with(fn _sql, _params -> {:ok, %{rows: [[true]]}} end)

    ticket =
      JOSE.JWT.sign(JOSE.JWK.from_oct("test_jwt_secret"), %{"alg" => "HS256"}, %{
        "sub" => "0b7d5c1e-6a1f-4c1e-9d5a-2f0c8e4b7a10",
        "sid" => UUID.uuid4(),
        "nonce" => "nonce",
        "purpose" => "ws",
        "session_id" => "1c8e6d2f-7b2a-4d2f-8e6b-3a1d9f5c8b21",
        "exp" => System.system_time(:second) + 600
      })
      |> JOSE.JWS.compact()
      |> elem(1)

    Counter.simulate_outage(true)
    assert {:error, :verification_error} = Auth.verify_jwt(ticket)
  end
end
