const {
  ECSClient, DescribeTaskDefinitionCommand, DescribeServicesCommand,
  RunTaskCommand, DescribeTasksCommand, UpdateServiceCommand,
} = require("@aws-sdk/client-ecs");
const { createHash } = require("node:crypto");

function configuration(env) {
  const cluster = env.AWS_ECS_CLUSTER;
  const match = /^arn:aws:ecs:us-east-1:(\d{12}):cluster\/([\w-]+)$/.exec(cluster || "");
  if (!match) throw new Error("AWS_ECS_CLUSTER must be a us-east-1 cluster ARN");
  const prefix = env.AWS_ECS_SERVICE_PREFIX || match[2];
  if (!/^[\w-]+$/.test(prefix)) throw new Error("Invalid service prefix");
  const definitions = {};
  const counts = {};
  for (const name of ["migration", "backend", "consumer"]) {
    const value = env[`AWS_${name.toUpperCase()}_TASK_DEFINITION`];
    const expected = `arn:aws:ecs:us-east-1:${match[1]}:task-definition/${prefix}-${name}:`;
    if (!value?.startsWith(expected) || !/^\d+$/.test(value.slice(expected.length))) {
      throw new Error(`Provide an explicit ${name} task definition revision`);
    }
    definitions[name] = value;
    if (name !== "migration") {
      const raw = env[`AWS_${name.toUpperCase()}_DESIRED_COUNT`] || "preserve";
      if (raw !== "preserve" && !/^[0-5]$/.test(raw)) throw new Error("Desired counts must be preserve or 0 through 5");
      counts[name] = raw === "preserve" ? undefined : Number(raw);
    }
  }
  return { cluster, prefix, definitions, counts, run: `${env.GITHUB_RUN_ID || "operator"}/${env.GITHUB_RUN_ATTEMPT || "1"}` };
}

function assertResponse(out) {
  if (out.failures?.length) throw new Error("ECS reported a resource or task-placement failure");
  return out;
}

async function waitForService(client, config, name, definition, sleep) {
  for (let attempt = 0; attempt < 180; attempt++) {
    const out = assertResponse(await client.send(new DescribeServicesCommand({ cluster: config.cluster, services: [`${config.prefix}-${name}`] })));
    const service = out.services?.[0];
    if (!service || service.deployments?.some((d) => d.rolloutState === "FAILED")) throw new Error(`${name} deployment failed`);
    if (service.deployments?.length === 1 && service.deployments[0].rolloutState === "COMPLETED") {
      if (service.taskDefinition !== definition) throw new Error(`${name} deployment rolled back`);
      if (service.runningCount === service.desiredCount && service.pendingCount === 0) return;
    }
    await sleep(5000);
  }
  throw new Error(`${name} deployment did not become healthy within 15 minutes`);
}

async function deploy(client, config, sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms)), report = console.log) {
  const definitions = {};
  for (const name of ["migration", "backend", "consumer"]) {
    const out = await client.send(new DescribeTaskDefinitionCommand({ taskDefinition: config.definitions[name] }));
    const definition = out.taskDefinition;
    const container = definition?.containerDefinitions?.find((c) => c.name === name);
    if (!container || definition.status !== "ACTIVE" || !/@sha256:[a-f0-9]{64}$/.test(container.image || "")) throw new Error(`Invalid or unpinned ${name} task definition`);
    definitions[name] = { definition, container };
  }
  const migration = definitions.migration.container;
  if (migration.image !== definitions.backend.container.image || migration.entryPoint?.join(" ") !== "/app/migrate") throw new Error("Migration must use /app/migrate from the exact backend image");
  const migrationSecret = migration.secrets?.find((s) => s.name === "PRIMARY_DB");
  const directSecret = definitions.backend.container.secrets?.find((s) => s.name === "PRIMARY_DB_DIRECT");
  if (!directSecret || directSecret.valueFrom !== migrationSecret?.valueFrom) throw new Error("Migration and backend must reference the same direct database secret version");
  if (!definitions.backend.container.environment?.some((e) => e.name === "DB_MIGRATIONS_ON_START" && e.value === "false")) throw new Error("Backend startup migrations must be disabled for this deployment");
  const current = assertResponse(await client.send(new DescribeServicesCommand({ cluster: config.cluster, services: [`${config.prefix}-backend`, `${config.prefix}-consumer`] })));
  if (current.services?.length !== 2 || current.services.some((s) => s.status !== "ACTIVE")) throw new Error("Both ECS services must already exist");
  const backend = current.services.find((s) => s.serviceName === `${config.prefix}-backend`);
  if (!backend?.networkConfiguration?.awsvpcConfiguration) throw new Error("Backend task networking is missing");
  const token = createHash("sha256").update(`${config.run}/${config.definitions.migration}`).digest("hex");
  const started = assertResponse(await client.send(new RunTaskCommand({
    cluster: config.cluster, taskDefinition: config.definitions.migration,
    launchType: "FARGATE", platformVersion: "1.4.0", count: 1,
    networkConfiguration: backend.networkConfiguration, clientToken: token,
    startedBy: `migration-${token.slice(0, 24)}`,
  })));
  const task = started.tasks?.[0]?.taskArn;
  if (!task) throw new Error("Migration task was not created");
  report(`Migration task: ${task}`);
  let migrated = false;
  for (let attempt = 0; attempt < 240; attempt++) {
    const out = assertResponse(await client.send(new DescribeTasksCommand({ cluster: config.cluster, tasks: [task] })));
    const status = out.tasks?.[0];
    if (!status) throw new Error("Migration task is missing");
    if (status.lastStatus === "STOPPED") {
      const container = status.containers?.find((c) => c.name === "migration");
      if (status.stopCode !== "EssentialContainerExited" || container?.exitCode !== 0) throw new Error("Migration failed; inspect its CloudWatch logs before retrying");
      migrated = true;
      break;
    }
    await sleep(5000);
  }
  if (!migrated) throw new Error("Migration status is unresolved; no services were updated. Inspect the running task before retrying");
  report("Migration completed successfully");
  const updated = [];
  try {
    for (const name of ["backend", "consumer"]) {
      const previous = current.services.find((s) => s.serviceName === `${config.prefix}-${name}`);
      updated.push(previous);
      await client.send(new UpdateServiceCommand({ cluster: config.cluster, service: previous.serviceName, taskDefinition: config.definitions[name], ...(config.counts[name] === undefined ? {} : { desiredCount: config.counts[name] }) }));
      await waitForService(client, config, name, config.definitions[name], sleep);
      report(`${name} service is stable on the requested revision`);
    }
  } catch (error) {
    for (const previous of updated.reverse()) {
      try {
        await client.send(new UpdateServiceCommand({ cluster: config.cluster, service: previous.serviceName, taskDefinition: previous.taskDefinition, desiredCount: previous.desiredCount }));
        report(`Restored previous deployment configuration for ${previous.serviceName}; verify its health`);
      } catch {
        report(`Rollback needs operator attention for ${previous.serviceName}`);
      }
    }
    throw error;
  }
}

if (require.main === module) {
  const client = new ECSClient({ region: "us-east-1", maxAttempts: 2 });
  Promise.resolve().then(() => deploy(client, configuration(process.env))).catch((error) => {
    console.error(error?.$metadata ? `AWS request failed: ${error.name}` : error.message);
    process.exitCode = 1;
  }).finally(() => client.destroy());
}

module.exports = { configuration, deploy };
