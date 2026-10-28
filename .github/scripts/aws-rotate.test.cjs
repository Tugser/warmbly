const { test } = require("node:test");
const assert = require("node:assert/strict");
const { rotationConfig, rotateKeys } = require("./aws-rotate.cjs");

const CLUSTER = "arn:aws:ecs:us-east-1:000000000000:cluster/fixture";
const V = (c) => c.repeat(32);

// world plays the rotator, the ops task and the deploy engine against a phase machine.
function world({ key = "INTERNAL_API_TOKEN", action = "rotate", state = {}, envLagging = 0, unsealed = false, holders } = {}) {
  const config = rotationConfig({ AWS_ECS_CLUSTER: CLUSTER, ROTATE_ACTION: action, ROTATE_KEY: key, ROTATE_NODE_WAIT_MINUTES: "4", GITHUB_RUN_ID: "9", GITHUB_RUN_ATTEMPT: "1" });
  const log = [];
  const tokens = new Set();
  let lag = envLagging;
  const owners = holders || (key.startsWith("CREDENTIALS") ? ["backend", "consumer"] : ["backend", "tracking"]);
  const versions = () => Object.fromEntries(owners.map((s, i) => [s, V(String(i + 1))]));
  const rotator = (act) => {
    log.push(`rotator ${act}`);
    if (act === "status") return { phase: state.phase || "none", key: state.key || null, versions: {}, pinned: state.key ? versions() : {}, until: state.until || null };
    if (act === "stage") {
      state = key === "CREDENTIALS_ENCRYPTION_KEY" && !state.key ? { key, phase: "introduced" } : { key, phase: "staged" };
      return { phase: state.phase, versions: versions(), holders: owners };
    }
    state = {};
    return { phase: "none", versions: act === "prepare" ? { backend: V("p") } : versions(), holders: owners };
  };
  const deps = {
    async describe(_, family) {
      return { definition: { taskDefinitionArn: `${family}:1`, family, containerDefinitions: [{ name: family.split("-").pop() }] } };
    },
    async runTask(_, cfg, network, arn, definition, label, sleep, report, options) {
      assert.ok(network.awsvpcConfiguration);
      const token = `${cfg.run}/${label}`;
      assert.ok(!tokens.has(token), `run token reused for ${label}`);
      tokens.add(token);
      if (arn.startsWith("fixture-rotator")) {
        const act = options.environment.find((e) => e.name === "ROTATE_ACTION").value;
        assert.equal(options.environment.find((e) => e.name === "ROTATE_KEY").value, key);
        const lines = ["phase written", `ROTATION_RESULT ${JSON.stringify(rotator(act))}`];
        assert.ok(options.capture(lines) && !options.capture(["phase written"]));
        return lines;
      }
      const command = options.command.join(" ");
      log.push(`ops ${command}`);
      if (command === "fleet env-status" && lag-- > 0) throw new Error("not current");
      if (command === "keys status --require-primary" && unsealed) throw new Error("on read key");
      return [];
    },
    async deploy(_, cfg) {
      assert.equal(cfg.mode, "refresh-secrets");
      assert.equal(cfg.fleet, false);
      assert.equal(cfg.tag, "");
      log.push(`roll ${cfg.services.join(",")}`);
      return {};
    },
  };
  const clients = { ecs: { async send() {
    return { services: [{ networkConfiguration: { awsvpcConfiguration: { subnets: ["s"] } } }] };
  } } };
  const report = [];
  const run = () => rotateKeys(clients, config, async () => {}, (l) => report.push(l), deps);
  return { run, log, report, state: () => state };
}

test("configuration refuses unknown keys, actions and waits", () => {
  const base = { AWS_ECS_CLUSTER: CLUSTER, ROTATE_ACTION: "rotate", ROTATE_KEY: "INTERNAL_API_TOKEN" };
  assert.throws(() => rotationConfig({ ...base, ROTATE_KEY: "AUTH_SECRET" }), /ROTATE_KEY/);
  assert.throws(() => rotationConfig({ ...base, ROTATE_ACTION: "delete" }), /ROTATE_ACTION/);
  assert.throws(() => rotationConfig({ ...base, ROTATE_NODE_WAIT_MINUTES: "999" }), /0 through 240/);
  assert.equal(rotationConfig(base).kind, "token");
});

test("a token is staged receiver first, waits for nodes, then retires in one run", async () => {
  const w = world({ envLagging: 1 });
  assert.deepEqual(await w.run(), { phase: "none" });
  assert.deepEqual(w.log, [
    "rotator status", "rotator stage", "roll backend,tracking",
    "ops fleet env-status", "ops fleet env-status", "rotator retire", "roll backend,tracking",
  ]);
});

test("a token stays staged when nodes never catch up, and says how to finish", async () => {
  const w = world({ envLagging: 99 });
  assert.deepEqual(await w.run(), { phase: "staged" });
  assert.ok(!w.log.includes("rotator retire"));
  assert.equal(w.log.filter((l) => l === "ops fleet env-status").length, 3);
  assert.match(w.report.at(-1), /is staged: .*fleet env-status/);
});

test("the credentials key is introduced, made primary once nodes read it, resealed, then retired", async () => {
  const w = world({ key: "CREDENTIALS_ENCRYPTION_KEY" });
  assert.deepEqual(await w.run(), { phase: "none" });
  assert.deepEqual(w.log, [
    "rotator status", "rotator stage", "roll backend,consumer",
    "ops fleet env-status", "rotator stage", "roll backend,consumer",
    "ops fleet env-status", "ops keys reseal --apply", "ops keys status --require-primary",
    "rotator retire", "roll backend,consumer",
  ]);
});

test("the credentials key waits at introduced when a node has not refreshed", async () => {
  const w = world({ key: "CREDENTIALS_ENCRYPTION_KEY", envLagging: 99 });
  assert.deepEqual(await w.run(), { phase: "introduced" });
  assert.equal(w.log.filter((l) => l === "rotator stage").length, 1);
});

test("a seal key is not retired while values still need the old key", async () => {
  const w = world({ key: "TWOFA_SECRET", unsealed: true, holders: ["backend"] });
  assert.deepEqual(await w.run(), { phase: "staged" });
  assert.ok(!w.log.includes("ops fleet env-status"));
  assert.ok(!w.log.includes("rotator retire"));
  assert.match(w.report.at(-1), /keys status/);
});

test("a signing secret stops at staged", async () => {
  const w = world({ key: "UNSUBSCRIBE_SIGNING_SECRET", holders: ["backend"] });
  assert.deepEqual(await w.run(), { phase: "staged" });
  assert.deepEqual(w.log, ["rotator status", "rotator stage", "roll backend"]);
  assert.match(w.report.at(-1), /for a year/);
});

test("another key mid-rotation refuses before anything is written", async () => {
  const w = world({ state: { key: "NODE_BROKER_TOKEN", phase: "staged" } });
  await assert.rejects(w.run(), /NODE_BROKER_TOKEN is mid-rotation/);
  assert.deepEqual(w.log, ["rotator status"]);
});

test("retire and abort refuse without a rotation and roll when there is one", async () => {
  await assert.rejects(world({ action: "retire" }).run(), /no staged rotation/);
  await assert.rejects(world({ action: "abort" }).run(), /no rotation/);
  const abort = world({ action: "abort", state: { key: "INTERNAL_API_TOKEN", phase: "staged" } });
  await abort.run();
  assert.deepEqual(abort.log, ["rotator status", "rotator abort", "roll backend,tracking"]);
  const retire = world({ action: "retire", state: { key: "INTERNAL_API_TOKEN", phase: "staged" } });
  await retire.run();
  assert.deepEqual(retire.log, ["rotator status", "roll backend,tracking", "ops fleet env-status", "rotator retire", "roll backend,tracking"]);
});

test("prepare rolls only what changed and says to reference the keys in Terraform", async () => {
  const w = world({ action: "prepare" });
  await w.run();
  assert.deepEqual(w.log, ["rotator status", "rotator prepare", "roll backend"]);
  assert.match(w.report.at(-1), /secret_keys/);
});

test("rotate resumes a run that stopped after staging instead of refusing", async () => {
  const w = world({ state: { key: "INTERNAL_API_TOKEN", phase: "staged" } });
  assert.deepEqual(await w.run(), { phase: "none" });
  assert.deepEqual(w.log, ["rotator status", "roll backend,tracking", "ops fleet env-status", "rotator retire", "roll backend,tracking"]);
  const sign = world({ key: "UNSUBSCRIBE_SIGNING_SECRET", holders: ["backend"], state: { key: "UNSUBSCRIBE_SIGNING_SECRET", phase: "staged", until: "2027-10-25T00:00:00.000Z" } });
  assert.deepEqual(await sign.run(), { phase: "staged" });
  assert.deepEqual(sign.log, ["rotator status", "roll backend"]);
  assert.match(sign.report.at(-1), /after 2027-10-25/);
});
