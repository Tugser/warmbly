defmodule Realtime.Test.Counter do
  @moduledoc """
  In-memory stand-in for the Redis counter behind `Realtime.RateLimiter`.

  Selected via `config :realtime, rate_limit_counter:` so the suite exercises
  real limiter decisions without a live Redis. `simulate_outage/1` returns nil
  the way `Realtime.Redis.incr_with_ttl/2` does when Redis is unreachable, which
  is what the limiter's fail-open path keys on.
  """

  use Agent

  def start_link(_opts \\ []) do
    Agent.start_link(fn -> %{counts: %{}, down: false} end, name: __MODULE__)
  end

  def reset do
    Agent.update(__MODULE__, fn _ -> %{counts: %{}, down: false} end)
  end

  def simulate_outage(down?) do
    Agent.update(__MODULE__, &Map.put(&1, :down, down?))
  end

  def count(key) do
    Agent.get(__MODULE__, &get_in(&1, [:counts, key])) || 0
  end

  def set_nx(key, _ttl_seconds) do
    Agent.get_and_update(__MODULE__, fn
      %{down: true} = state ->
        {{:error, :redis_unavailable}, state}

      %{counts: counts} = state ->
        if Map.has_key?(counts, key),
          do: {{:ok, nil}, state},
          else: {{:ok, "OK"}, %{state | counts: Map.put(counts, key, 1)}}
    end)
  end

  def incr_with_ttl(key, _ttl_seconds) do
    Agent.get_and_update(__MODULE__, fn
      %{down: true} = state ->
        {nil, state}

      %{counts: counts} = state ->
        next = Map.get(counts, key, 0) + 1
        {next, %{state | counts: Map.put(counts, key, next)}}
    end)
  end
end
