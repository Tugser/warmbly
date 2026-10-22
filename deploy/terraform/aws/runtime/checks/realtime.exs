try do
  {:ok, _} = Application.ensure_all_started(:ecto_sql)
  {:ok, _} = Application.ensure_all_started(:postgrex)

  {:ok, _} =
    Realtime.Repo.start_link(
      pool_size: 2,
      show_sensitive_data_on_connection_error: false
    )

  for value <- 1..4 do
    {:ok, %{rows: [[^value]]}} =
      Ecto.Adapters.SQL.query(Realtime.Repo, "SELECT $1::integer", [value], log: false)
  end

  IO.puts("Ecto/Postgrex pooled prepared queries succeeded")
rescue
  _ ->
    IO.puts("Realtime database client check failed; connection details omitted")
    System.halt(1)
end
