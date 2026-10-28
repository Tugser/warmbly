defmodule RealtimeWeb.Endpoint do
  @moduledoc """
  Phoenix Endpoint for WebSocket connections only.

  This service is a pure WebSocket gateway - no HTTP endpoints.
  All HTTP traffic should be handled by the Go backend.
  """

  use Phoenix.Endpoint, otp_app: :realtime

  # check_origin is deliberately absent here. A transport-level value takes
  # precedence over the endpoint's, so hardcoding one made the CHECK_ORIGIN
  # environment variable, and the installer flag that sets it, do nothing.
  # The endpoint config in runtime.exs governs it now.
  socket("/socket", RealtimeWeb.UserSocket,
    websocket: [
      timeout: 60_000,
      compress: true,
      # Client frames are small (joins, presence, live cursor/select/patch);
      # without a cap cowboy accepts unbounded frames, which compress makes
      # cheap to send and the org fan-out makes expensive to receive.
      max_frame_size: 65_536,
      # x_headers carries the API key / OAuth token header; peer_data the client IP.
      connect_info: [:peer_data, :x_headers],
      error_handler: {RealtimeWeb.UserSocket, :handle_error, []}
    ],
    longpoll: false
  )

  # Minimal plugs for WebSocket upgrade handling
  plug(Plug.RequestId)
  plug(Plug.Telemetry, event_prefix: [:realtime, :endpoint])

  # Health check endpoint for Docker/load balancer probes
  plug(:health_check)
  plug(:not_found)

  defp health_check(%Plug.Conn{request_path: "/health"} = conn, _opts) do
    conn
    |> Plug.Conn.send_resp(200, "ok")
    |> Plug.Conn.halt()
  end

  defp health_check(conn, _opts), do: conn

  # Every other HTTP path is a plain 404; the socket upgrade is handled before the plugs run.
  defp not_found(conn, _opts) do
    conn
    |> Plug.Conn.send_resp(404, "")
    |> Plug.Conn.halt()
  end
end
