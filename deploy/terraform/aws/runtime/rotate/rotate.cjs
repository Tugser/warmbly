// Runs inside AWS as a one-off task. Generates replacement keys, writes them
// into the service secrets as new versions, and keeps the previous values
// readable until they are retired. It logs key names, phases and version ids,
// never a value; the last line is ROTATION_RESULT with the same.
const { createHash, randomBytes } = require("node:crypto");

const DAY = 24 * 60 * 60 * 1000;
// kind: seal = stored ciphertext, sign = issued links, token = shared bearer token.
const KEYS = {
  CREDENTIALS_ENCRYPTION_KEY: { kind: "seal", read: "CREDENTIALS_ENCRYPTION_READ_KEYS", introduce: true },
  TWOFA_SECRET: { kind: "seal", read: "TWOFA_READ_SECRETS" },
  UNSUBSCRIBE_SIGNING_SECRET: { kind: "sign", read: "UNSUBSCRIBE_SIGNING_READ_SECRETS", until: "UNSUBSCRIBE_SIGNING_READ_UNTIL", window: 366 * DAY },
  INTERNAL_API_TOKEN: { kind: "token", read: "INTERNAL_API_PREVIOUS_TOKENS", until: "INTERNAL_API_PREVIOUS_UNTIL", window: 7 * DAY, receivers: ["backend"] },
  NODE_BROKER_TOKEN: { kind: "token", read: "NODE_BROKER_PREVIOUS_TOKENS", until: "NODE_BROKER_PREVIOUS_UNTIL", window: 7 * DAY, receivers: ["backend"] },
};
const MAX_READ = 4;

class RotationError extends Error {}

function list(raw) {
  if (raw === undefined || raw === null || String(raw).trim() === "") return [];
  const parsed = JSON.parse(raw);
  if (!Array.isArray(parsed) || parsed.some((v) => typeof v !== "string" || !v)) throw new RotationError("a read list is not a JSON array of strings");
  return parsed;
}

function withRead(values, add, drop = []) {
  const out = [...add, ...values.filter((v) => !add.includes(v) && !drop.includes(v))];
  if (out.length > MAX_READ) throw new RotationError(`the read list would hold more than ${MAX_READ} values; retire the previous rotation first`);
  return JSON.stringify(out);
}

const fresh = () => randomBytes(32).toString("hex");

function makeStore(client, commands) {
  const { GetSecretValueCommand, PutSecretValueCommand } = commands;
  return {
    async read(arn, versionId) {
      try {
        const out = await client.send(new GetSecretValueCommand({ SecretId: arn, ...(versionId ? { VersionId: versionId } : {}) }));
        return { versionId: out.VersionId, value: out.SecretString ? JSON.parse(out.SecretString) : {} };
      } catch (error) {
        if (error?.name === "ResourceNotFoundException") return { versionId: null, value: {} };
        throw error;
      }
    },
    async write(arn, value, token) {
      const out = await client.send(new PutSecretValueCommand({ SecretId: arn, SecretString: JSON.stringify(value), ClientRequestToken: token }));
      return out.VersionId;
    },
  };
}

async function rotate({ store, action, key, secrets, stateArn, run = "local", now = Date.now(), log = console.log }) {
  const spec = KEYS[key];
  if (action !== "status" && !spec) throw new RotationError(`unknown key ${key}; expected one of ${Object.keys(KEYS).join(", ")}`);
  const state = (await store.read(stateArn)).value;
  const versions = {};
  const token = (service) => createHash("sha256").update(`${run}/${action}/${key}/${state.phase || "none"}/${service}`).digest("hex").replace(/^(.{8})(.{4})(.{4})(.{4})(.{12}).*/, "$1-$2-$3-$4-$5");

  // holders are the services whose secret carries the key at all.
  const loaded = {};
  for (const [service, arn] of Object.entries(secrets)) loaded[service] = await store.read(arn);
  const holders = spec ? Object.keys(loaded).filter((s) => typeof loaded[s].value[key] === "string" && loaded[s].value[key]) : [];
  if (spec && !holders.length && action !== "status") throw new RotationError(`no service secret holds ${key}`);
  const readers = (s) => !spec.receivers || spec.receivers.includes(s);

  const save = async (service, value) => {
    versions[service] = await store.write(secrets[service], value, token(service));
    log(`${service}: wrote secret version ${versions[service]}`);
  };
  const saveState = async (next) => {
    await store.write(stateArn, next, token(`state/${next.phase || "clear"}`));
    log(`rotation state: ${next.key ? `${next.key} ${next.phase}` : "clear"}`);
  };
  const busy = state.key && state.key !== key;

  switch (action) {
  case "status":
    log(state.key ? `${state.key} is ${state.phase} since ${state.updatedAt}` : "no rotation in progress");
    return { phase: state.phase || "none", key: state.key || null, versions, pinned: state.versions || {}, until: state.until || null };

  case "prepare": {
    // Overlap keys must exist before Terraform may reference them.
    for (const service of holders) {
      const value = { ...loaded[service].value };
      let changed = false;
      if (readers(service) && value[spec.read] === undefined) { value[spec.read] = "[]"; changed = true; }
      if (readers(service) && spec.until && value[spec.until] === undefined) { value[spec.until] = ""; changed = true; }
      if (changed) await save(service, value);
      else log(`${service}: already carries the overlap keys`);
    }
    return { phase: state.key === key ? state.phase : "none", versions, holders };
  }

  case "stage": {
    if (busy) throw new RotationError(`${state.key} is mid-rotation; finish or abort it first`);
    for (const service of holders) {
      if (readers(service) && loaded[service].value[spec.read] === undefined) {
        throw new RotationError(`${service} has no ${spec.read}; run prepare and add it to that service's Terraform secret_keys first`);
      }
    }
    const before = state.before || Object.fromEntries(holders.map((s) => [s, loaded[s].versionId]));
    const updatedAt = new Date(now).toISOString();
    // The intent is saved before the first write, so a run that dies part way is finished by the next with the same values.
    if (spec.introduce && (!state.key || state.phase === "introducing")) {
      // Every reader learns the new key before anything writes with it.
      const pending = state.pending || fresh();
      const previous = state.previous || loaded[holders[0]].value[key];
      if (!state.key) await saveState({ key, phase: "introducing", pending, previous, before, versions: {}, updatedAt });
      for (const service of holders) {
        const value = { ...loaded[service].value };
        value[spec.read] = withRead(list(value[spec.read]), [pending]);
        await save(service, value);
      }
      await saveState({ key, phase: "introduced", pending, previous, before, versions, updatedAt });
      return { phase: "introduced", versions, holders };
    }
    if (state.key && state.phase !== "introduced" && state.phase !== "staging") throw new RotationError(`${key} is already ${state.phase}; retire or abort it`);
    const next = state.next || state.pending || fresh();
    const previous = state.previous || loaded[holders[0]].value[key];
    if (holders.some((s) => ![previous, next].includes(loaded[s].value[key]))) throw new RotationError(`services disagree on the current ${key}; align them before rotating`);
    const until = state.until || (spec.until ? new Date(now + spec.window).toISOString() : null);
    if (state.phase !== "staging") await saveState({ key, phase: "staging", next, previous, before, until, versions: state.versions || {}, updatedAt });
    for (const service of holders) {
      const value = { ...loaded[service].value, [key]: next };
      if (readers(service)) {
        value[spec.read] = withRead(list(value[spec.read]), [previous], [next]);
        if (until) value[spec.until] = until;
      }
      await save(service, value);
    }
    await saveState({ key, phase: "staged", next, previous, before, until, versions, updatedAt });
    return { phase: "staged", versions, holders, until };
  }

  case "retire": {
    if (state.key !== key || state.phase !== "staged") throw new RotationError(`${key} has no staged rotation to retire`);
    if (state.until && spec.kind === "sign" && now < Date.parse(state.until)) {
      throw new RotationError(`links signed with the previous ${key} stay valid until ${state.until}; retire after that`);
    }
    for (const service of holders) {
      const value = { ...loaded[service].value };
      if (!readers(service)) continue;
      const remaining = list(value[spec.read]).filter((v) => v !== state.previous);
      value[spec.read] = JSON.stringify(remaining);
      if (spec.until && !remaining.length) value[spec.until] = "";
      await save(service, value);
    }
    await saveState({});
    return { phase: "none", versions, holders };
  }

  case "abort": {
    if (state.key !== key) throw new RotationError(`no rotation of ${key} is in progress`);
    for (const service of holders) {
      const value = { ...loaded[service].value };
      if (state.phase === "introducing" || state.phase === "introduced") {
        if (!readers(service)) continue;
        value[spec.read] = withRead(list(value[spec.read]), [], [state.pending]);
      } else {
        // staging or staged: a service the stage never reached already holds the previous value.
        const replacement = state.next;
        value[key] = state.previous;
        if (readers(service)) {
          // A token is forgotten; a key stays readable because something may already be sealed or signed with it.
          const keep = spec.kind === "token" ? [] : [replacement];
          value[spec.read] = withRead(list(value[spec.read]), keep, [state.previous, replacement]);
          if (spec.until && !keep.length && !list(value[spec.read]).length) value[spec.until] = "";
        }
      }
      await save(service, value);
    }
    await saveState({});
    return { phase: "none", versions, holders };
  }

  default:
    throw new RotationError(`unknown action ${action}`);
  }
}

if (require.main === module) {
  const sdk = require("@aws-sdk/client-secrets-manager");
  const client = new sdk.SecretsManagerClient({ region: process.env.AWS_REGION || "us-east-1" });
  rotate({
    store: makeStore(client, sdk),
    action: process.env.ROTATE_ACTION,
    key: process.env.ROTATE_KEY,
    secrets: JSON.parse(process.env.SERVICE_SECRET_ARNS || "{}"),
    stateArn: process.env.ROTATION_SECRET_ARN,
    run: process.env.ROTATE_RUN || "local",
  }).then((result) => {
    console.log(`ROTATION_RESULT ${JSON.stringify(result)}`);
  }).catch((error) => {
    console.error(error instanceof RotationError ? error.message : `rotation failed: ${error?.name || "error"}`);
    process.exitCode = 1;
  });
}

module.exports = { rotate, makeStore, KEYS, RotationError };
