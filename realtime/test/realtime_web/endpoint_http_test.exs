defmodule RealtimeWeb.EndpointHttpTest do
  use ExUnit.Case, async: true
  import Plug.Test

  test "the health probe answers 200" do
    conn = RealtimeWeb.Endpoint.call(conn(:get, "/health"), [])
    assert conn.status == 200
    assert conn.resp_body == "ok"
  end

  test "any other HTTP path answers an empty 404" do
    for path <- ["/", "/healthz", "/api/health", "/.git/HEAD", "/socket"] do
      conn = RealtimeWeb.Endpoint.call(conn(:get, path), [])
      assert conn.status == 404, "#{path} answered #{conn.status}"
      assert conn.resp_body == ""
    end
  end
end
