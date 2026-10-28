const { test } = require("node:test");
const assert = require("node:assert/strict");
const { rotate, makeStore } = require("./rotate.cjs");

const OLD = "o".repeat(64);
const STATE = "arn:state";

function memory(initial) {
  const secrets = {};
  let n = 0;
  for (const [arn, value] of Object.entries(initial)) secrets[arn] = [{ id: `v${n++}`, value: structuredClone(value) }];
  const tokens = new Set();
  const faults = { failWrite: null };
  const client = { async send(command) {
    const name = command.constructor.name;
    const versions = secrets[command.input.SecretId];
    if (name === "GetSecretValueCommand") {
      if (!versions) throw Object.assign(new Error("missing"), { name: "ResourceNotFoundException" });
      const v = command.input.VersionId ? versions.find((x) => x.id === command.input.VersionId) : versions.at(-1);
      return { VersionId: v.id, SecretString: JSON.stringify(v.value) };
    }
    if (name === "PutSecretValueCommand") {
      if (faults.failWrite?.(command.input.SecretId)) { faults.failWrite = null; throw Object.assign(new Error("throttled"), { name: "ThrottlingException" }); }
      assert.ok(!tokens.has(command.input.ClientRequestToken), "request tokens are unique per write");
      assert.match(command.input.ClientRequestToken, /^[0-9a-f-]{36}$/);
      tokens.add(command.input.ClientRequestToken);
      const id = `v${n++}`;
      (secrets[command.input.SecretId] ||= []).push({ id, value: JSON.parse(command.input.SecretString) });
      return { VersionId: id };
    }
    throw new Error(name);
  } };
  class GetSecretValueCommand { constructor(input) { this.input = input; } }
  class PutSecretValueCommand { constructor(input) { this.input = input; } }
  return { store: makeStore(client, { GetSecretValueCommand, PutSecretValueCommand }), current: (arn) => secrets[arn]?.at(-1).value, faults };
}

function setup(extra = {}) {
  const backend = { CREDENTIALS_ENCRYPTION_KEY: OLD, CREDENTIALS_ENCRYPTION_READ_KEYS: "[]", INTERNAL_API_TOKEN: OLD, INTERNAL_API_PREVIOUS_TOKENS: "[]", INTERNAL_API_PREVIOUS_UNTIL: "", UNSUBSCRIBE_SIGNING_SECRET: OLD, UNSUBSCRIBE_SIGNING_READ_SECRETS: "", UNSUBSCRIBE_SIGNING_READ_UNTIL: "", PRIMARY_DB: "postgres://fixture", ...extra.backend };
  const consumer = { CREDENTIALS_ENCRYPTION_KEY: OLD, CREDENTIALS_ENCRYPTION_READ_KEYS: "[]", ...extra.consumer };
  const tracking = { INTERNAL_API_TOKEN: OLD, ...extra.tracking };
  const m = memory({ "arn:backend": backend, "arn:consumer": consumer, "arn:tracking": tracking });
  const logs = [];
  const call = (action, key, run = "r1", now = Date.parse("2026-10-24T00:00:00Z")) => rotate({
    store: m.store, action, key, run: `${run}/${action}/${Math.random()}`, now, stateArn: STATE,
    secrets: { backend: "arn:backend", consumer: "arn:consumer", tracking: "arn:tracking" }, log: (l) => logs.push(l),
  });
  return { ...m, logs, call };
}

test("the credentials key is introduced to every reader before it becomes primary, then retired", async () => {
  const t = setup();
  const one = await t.call("stage", "CREDENTIALS_ENCRYPTION_KEY");
  assert.equal(one.phase, "introduced");
  assert.deepEqual(Object.keys(one.versions).sort(), ["backend", "consumer"]);
  const [pending] = JSON.parse(t.current("arn:backend").CREDENTIALS_ENCRYPTION_READ_KEYS);
  assert.match(pending, /^[0-9a-f]{64}$/);
  assert.equal(t.current("arn:backend").CREDENTIALS_ENCRYPTION_KEY, OLD);
  assert.deepEqual(JSON.parse(t.current("arn:consumer").CREDENTIALS_ENCRYPTION_READ_KEYS), [pending]);

  const two = await t.call("stage", "CREDENTIALS_ENCRYPTION_KEY");
  assert.equal(two.phase, "staged");
  for (const arn of ["arn:backend", "arn:consumer"]) {
    assert.equal(t.current(arn).CREDENTIALS_ENCRYPTION_KEY, pending);
    assert.deepEqual(JSON.parse(t.current(arn).CREDENTIALS_ENCRYPTION_READ_KEYS), [OLD]);
  }
  await assert.rejects(t.call("stage", "INTERNAL_API_TOKEN"), /mid-rotation/);
  await t.call("retire", "CREDENTIALS_ENCRYPTION_KEY");
  assert.deepEqual(JSON.parse(t.current("arn:backend").CREDENTIALS_ENCRYPTION_READ_KEYS), []);
  assert.equal(t.current("arn:backend").PRIMARY_DB, "postgres://fixture");
  assert.deepEqual(t.current(STATE), {});
});

test("a token keeps the previous value only on its receiver, with a window", async () => {
  const t = setup();
  const out = await t.call("stage", "INTERNAL_API_TOKEN");
  const fresh = t.current("arn:backend").INTERNAL_API_TOKEN;
  assert.notEqual(fresh, OLD);
  assert.equal(t.current("arn:tracking").INTERNAL_API_TOKEN, fresh);
  assert.deepEqual(JSON.parse(t.current("arn:backend").INTERNAL_API_PREVIOUS_TOKENS), [OLD]);
  assert.equal(t.current("arn:backend").INTERNAL_API_PREVIOUS_UNTIL, "2026-10-31T00:00:00.000Z");
  assert.equal(t.current("arn:tracking").INTERNAL_API_PREVIOUS_TOKENS, undefined);
  assert.deepEqual(Object.keys(out.versions).sort(), ["backend", "tracking"]);
  await t.call("retire", "INTERNAL_API_TOKEN");
  assert.equal(t.current("arn:backend").INTERNAL_API_PREVIOUS_TOKENS, "[]");
  assert.equal(t.current("arn:backend").INTERNAL_API_PREVIOUS_UNTIL, "");
});

test("aborting a token restores the old value everywhere and forgets the new one", async () => {
  const t = setup();
  await t.call("stage", "INTERNAL_API_TOKEN");
  await t.call("abort", "INTERNAL_API_TOKEN");
  assert.equal(t.current("arn:backend").INTERNAL_API_TOKEN, OLD);
  assert.equal(t.current("arn:tracking").INTERNAL_API_TOKEN, OLD);
  assert.deepEqual(JSON.parse(t.current("arn:backend").INTERNAL_API_PREVIOUS_TOKENS), []);
});

test("aborting a staged key keeps the replacement readable", async () => {
  const t = setup();
  await t.call("stage", "CREDENTIALS_ENCRYPTION_KEY");
  await t.call("stage", "CREDENTIALS_ENCRYPTION_KEY");
  const replacement = t.current("arn:backend").CREDENTIALS_ENCRYPTION_KEY;
  await t.call("abort", "CREDENTIALS_ENCRYPTION_KEY");
  assert.equal(t.current("arn:backend").CREDENTIALS_ENCRYPTION_KEY, OLD);
  assert.deepEqual(JSON.parse(t.current("arn:consumer").CREDENTIALS_ENCRYPTION_READ_KEYS), [replacement]);
});

test("a signing secret cannot be retired while links signed with it are valid", async () => {
  const t = setup();
  await t.call("stage", "UNSUBSCRIBE_SIGNING_SECRET");
  await assert.rejects(t.call("retire", "UNSUBSCRIBE_SIGNING_SECRET"), /stay valid until 2027-10-25/);
  await t.call("retire", "UNSUBSCRIBE_SIGNING_SECRET", "r2", Date.parse("2027-10-26T00:00:00Z"));
  assert.equal(t.current("arn:backend").UNSUBSCRIBE_SIGNING_READ_UNTIL, "");
});

test("prepare adds empty overlap keys once, and stage refuses without them", async () => {
  const t = setup({ backend: { TWOFA_SECRET: OLD } });
  await assert.rejects(t.call("stage", "TWOFA_SECRET"), /run prepare/);
  const first = await t.call("prepare", "TWOFA_SECRET");
  assert.deepEqual(Object.keys(first.versions), ["backend"]);
  assert.equal(t.current("arn:backend").TWOFA_READ_SECRETS, "[]");
  const again = await t.call("prepare", "TWOFA_SECRET");
  assert.deepEqual(again.versions, {});
});

test("a full read list refuses instead of dropping a key", async () => {
  const full = JSON.stringify(["a", "b", "c", "d"].map((c) => c.repeat(64)));
  const t = setup({ backend: { CREDENTIALS_ENCRYPTION_READ_KEYS: full } });
  await assert.rejects(t.call("stage", "CREDENTIALS_ENCRYPTION_KEY"), /more than 4/);
});

test("no generated or stored value reaches the log or the result", async () => {
  const t = setup();
  const results = [];
  results.push(await t.call("stage", "CREDENTIALS_ENCRYPTION_KEY"));
  results.push(await t.call("stage", "CREDENTIALS_ENCRYPTION_KEY"));
  results.push(await t.call("stage", "INTERNAL_API_TOKEN").catch((e) => e.message));
  const secrets = [OLD, t.current("arn:backend").CREDENTIALS_ENCRYPTION_KEY, "postgres://fixture"];
  const text = t.logs.join("\n") + JSON.stringify(results);
  for (const s of secrets) assert.ok(!text.includes(s));
});

test("a stage that dies part way is finished by the next with the same key", async () => {
  const t = setup();
  await t.call("stage", "CREDENTIALS_ENCRYPTION_KEY");
  const [pending] = JSON.parse(t.current("arn:backend").CREDENTIALS_ENCRYPTION_READ_KEYS);
  t.faults.failWrite = (arn) => arn === "arn:consumer";
  await assert.rejects(t.call("stage", "CREDENTIALS_ENCRYPTION_KEY"), /throttled|ThrottlingException/);
  assert.equal(t.current("arn:backend").CREDENTIALS_ENCRYPTION_KEY, pending);
  assert.equal(t.current("arn:consumer").CREDENTIALS_ENCRYPTION_KEY, OLD);
  assert.equal(t.current(STATE).phase, "staging");
  const done = await t.call("stage", "CREDENTIALS_ENCRYPTION_KEY");
  assert.equal(done.phase, "staged");
  for (const arn of ["arn:backend", "arn:consumer"]) {
    assert.equal(t.current(arn).CREDENTIALS_ENCRYPTION_KEY, pending);
    assert.deepEqual(JSON.parse(t.current(arn).CREDENTIALS_ENCRYPTION_READ_KEYS), [OLD]);
  }
});

test("aborting a stage that died part way puts every service back on one primary", async () => {
  const t = setup();
  await t.call("stage", "CREDENTIALS_ENCRYPTION_KEY");
  const [pending] = JSON.parse(t.current("arn:backend").CREDENTIALS_ENCRYPTION_READ_KEYS);
  t.faults.failWrite = (arn) => arn === "arn:consumer";
  await assert.rejects(t.call("stage", "CREDENTIALS_ENCRYPTION_KEY"));
  await t.call("abort", "CREDENTIALS_ENCRYPTION_KEY");
  for (const arn of ["arn:backend", "arn:consumer"]) {
    assert.equal(t.current(arn).CREDENTIALS_ENCRYPTION_KEY, OLD);
    assert.deepEqual(JSON.parse(t.current(arn).CREDENTIALS_ENCRYPTION_READ_KEYS), [pending]);
  }
  assert.deepEqual(t.current(STATE), {});
});

test("a token stage that dies part way keeps the token it generated", async () => {
  const t = setup();
  t.faults.failWrite = (arn) => arn === "arn:tracking";
  await assert.rejects(t.call("stage", "INTERNAL_API_TOKEN"));
  const fresh = t.current("arn:backend").INTERNAL_API_TOKEN;
  assert.notEqual(fresh, OLD);
  assert.equal(t.current("arn:tracking").INTERNAL_API_TOKEN, OLD);
  await t.call("stage", "INTERNAL_API_TOKEN");
  assert.equal(t.current("arn:tracking").INTERNAL_API_TOKEN, fresh);
  assert.deepEqual(JSON.parse(t.current("arn:backend").INTERNAL_API_PREVIOUS_TOKENS), [OLD]);
});
