// Drives a key rotation from GitHub without ever holding a value: the rotator
// task writes new secret versions inside AWS and reports their ids, the deploy
// engine rolls services onto exactly those versions, and warmblyctl in the ops
// task proves each step is safe before the old value is retired.
const { DescribeServicesCommand } = require("@aws-sdk/client-ecs");
const engine = require("./aws-deploy.cjs");

const { configuration, SERVICES } = engine;

const KINDS = {
  CREDENTIALS_ENCRYPTION_KEY: "seal",
  TWOFA_SECRET: "seal",
  UNSUBSCRIBE_SIGNING_SECRET: "sign",
  INTERNAL_API_TOKEN: "token",
  NODE_BROKER_TOKEN: "token",
};
const ACTIONS = ["status", "prepare", "rotate", "retire", "abort"];

function rotationConfig(env) {
  const action = env.ROTATE_ACTION;
  const key = env.ROTATE_KEY;
  if (!ACTIONS.includes(action)) throw new Error(`ROTATE_ACTION must be one of ${ACTIONS.join(", ")}`);
  if (!KINDS[key]) throw new Error(`ROTATE_KEY must be one of ${Object.keys(KINDS).join(", ")}`);
  const wait = Number(env.ROTATE_NODE_WAIT_MINUTES ?? 30);
  if (!Number.isInteger(wait) || wait < 0 || wait > 240) throw new Error("ROTATE_NODE_WAIT_MINUTES must be 0 through 240");
  const base = configuration({ ...env, DEPLOY_MODE: "refresh-secrets", DEPLOY_TAG: "", DEPLOY_SERVICES: "all", DEPLOY_SECRET_VERSIONS: "", DEPLOY_IMAGES_JSON: "", DEPLOY_FLEET: "false" });
  return { ...base, action, key, kind: KINDS[key], nodeWaitMs: wait * 60000 };
}

async function rotateKeys(clients, config, sleep = (ms) => new Promise((r) => setTimeout(r, ms)), report = console.log, deps = engine) {
  const { deploy, runTask, describe } = deps;
  // ECS run tokens are idempotent, so every task and roll in one run needs its own.
  let seq = 0;
  const step = () => `${config.run}/${++seq}`;
  const services = await clients.ecs.send(new DescribeServicesCommand({ cluster: config.cluster, services: [`${config.prefix}-backend`] }));
  const network = services.services?.[0]?.networkConfiguration;
  if (!network?.awsvpcConfiguration) throw new Error("Backend task networking is missing");

  const rotator = async (action) => {
    const { definition } = await describe(clients.ecs, `${config.prefix}-rotator`);
    const environment = [
      { name: "ROTATE_ACTION", value: action }, { name: "ROTATE_KEY", value: config.key }, { name: "ROTATE_RUN", value: `${step()}/${action}` },
    ];
    const lines = await runTask(clients, { ...config, run: step() }, network, definition.taskDefinitionArn, definition, `Rotator ${action}`, sleep, report, { environment, capture: (ls) => ls.some((l) => l.startsWith("ROTATION_RESULT ")) });
    const line = lines.filter((l) => l.startsWith("ROTATION_RESULT ")).pop();
    if (!line) throw new Error(`the rotator reported no result for ${action}`);
    const result = JSON.parse(line.slice("ROTATION_RESULT ".length));
    for (const l of lines) if (!l.startsWith("ROTATION_RESULT ")) report(`  rotator: ${l}`);
    return result;
  };
  const ops = async (label, command) => {
    const { definition } = await describe(clients.ecs, `${config.prefix}-ops`);
    return runTask(clients, { ...config, run: step() }, network, definition.taskDefinitionArn, definition, label, sleep, report, { command });
  };
  const roll = async (versions) => {
    const names = Object.keys(versions || {}).filter((s) => SERVICES.includes(s));
    if (!names.length) return;
    report(`Rolling ${names.join(", ")} onto the new secret versions`);
    await deploy(clients, { ...config, run: step(), services: names, allServices: false, secretVersions: versions }, sleep, report);
  };
  const nodesCurrent = async () => {
    const checks = Math.floor(config.nodeWaitMs / 120000) + 1;
    for (let i = 1; ; i++) {
      try {
        await ops("Fleet env status", ["fleet", "env-status"]);
        report("Every live node has applied the new env");
        return true;
      } catch {
        if (i >= checks) return false;
        report("Some nodes have not applied the new env yet; checking again in two minutes");
        await sleep(120000);
      }
    }
  };
  const resealed = async () => {
    await ops("Reseal", ["keys", "reseal", "--apply"]);
    try {
      await ops("Key status", ["keys", "status", "--require-primary"]);
      return true;
    } catch {
      return false;
    }
  };
  const retire = async () => {
    const result = await rotator("retire");
    await roll(result.versions);
    report(`${config.key} rotated; the previous value is retired`);
  };
  // Nodes first: a node still on the old env would seal under the old key after the reseal.
  const gate = async () => {
    if ((config.kind === "token" || config.key === "CREDENTIALS_ENCRYPTION_KEY") && !(await nodesCurrent())) {
      return "some nodes have not applied the new env and still work on the previous value. Run retire once `warmblyctl fleet env-status` is clean";
    }
    if (config.kind === "seal" && !(await resealed())) return `some values still open only under the previous ${config.key}; run retire once \`warmblyctl keys status\` is clean`;
    return null;
  };

  const status = await rotator("status");
  if (status.key && status.key !== config.key) throw new Error(`${status.key} is mid-rotation; finish or abort it first`);
  let phase = status.key === config.key ? status.phase : "none";
  report(`${config.key}: ${phase}`);
  // A run that died after writing resumes here: rolling versions already live is a no-op.
  const resume = () => roll(status.pinned);

  switch (config.action) {
  case "status":
    if (config.kind === "seal") await ops("Key status", ["keys", "status"]).catch(() => report("keys status reports unreadable values"));
    return { phase };
  case "prepare": {
    const result = await rotator("prepare");
    await roll(result.versions);
    report(`Overlap keys exist for ${config.key}. Add them to secret_keys for ${result.holders.join(", ")} and apply the runtime Terraform before rotating.`);
    return { phase };
  }
  case "abort": {
    if (phase === "none") throw new Error(`no rotation of ${config.key} is in progress`);
    const result = await rotator("abort");
    await roll(result.versions);
    report(`${config.key} restored to its previous value`);
    return { phase: "none" };
  }
  case "retire": {
    if (phase !== "staged") throw new Error(`${config.key} has no staged rotation to retire`);
    await resume();
    const blocked = await gate();
    if (blocked) throw new Error(blocked);
    await retire();
    return { phase: "none" };
  }
  case "rotate": {
    if (phase !== "none") await resume();
    if (phase === "staged" && config.kind === "sign") {
      report(`${config.key} is staged; run retire after ${status.until}`);
      return { phase };
    }
    // introducing and staging are a stage that died part way; staging again finishes it with the saved values.
    if (phase === "none" || phase === "introducing") {
      const result = await rotator("stage");
      await roll(result.versions);
      phase = result.phase;
    }
    if (phase === "introduced" || phase === "staging") {
      if (phase === "introduced" && !(await nodesCurrent())) {
        report("The new key is readable everywhere except nodes that have not refreshed; run rotate again to make it primary");
        return { phase };
      }
      const result = await rotator("stage");
      await roll(result.versions);
      phase = result.phase;
    }
    if (config.kind === "sign") {
      report(`${config.key} rotated. Links signed with the previous value keep working for a year, the life of a link; run retire after that`);
      return { phase };
    }
    const blocked = await gate();
    if (blocked) {
      report(`${config.key} is staged: ${blocked}`);
      return { phase };
    }
    await retire();
    return { phase: "none" };
  }
  }
  return { phase };
}

if (require.main === module) {
  const { ECSClient } = require("@aws-sdk/client-ecs");
  const { SecretsManagerClient } = require("@aws-sdk/client-secrets-manager");
  const { CloudWatchClient } = require("@aws-sdk/client-cloudwatch");
  const { CloudWatchLogsClient } = require("@aws-sdk/client-cloudwatch-logs");
  const region = { region: "us-east-1", maxAttempts: 3 };
  const clients = { ecs: new ECSClient(region), secrets: new SecretsManagerClient(region), cloudwatch: new CloudWatchClient(region), logs: new CloudWatchLogsClient(region) };
  const lines = [];
  const report = (line) => { console.log(line); lines.push(line); };
  Promise.resolve().then(() => rotateKeys(clients, rotationConfig(process.env), undefined, report)).catch((error) => {
    const message = error?.$metadata ? `AWS request failed: ${error.name}` : error.message;
    console.error(message);
    lines.push(`**Failed:** ${message}`);
    process.exitCode = 1;
  }).finally(() => {
    if (process.env.GITHUB_STEP_SUMMARY) {
      require("node:fs").appendFileSync(process.env.GITHUB_STEP_SUMMARY, `### Key rotation\n\n\`\`\`\n${lines.join("\n")}\n\`\`\`\n`);
    }
    for (const c of Object.values(clients)) c.destroy();
  });
}

module.exports = { rotationConfig, rotateKeys };
