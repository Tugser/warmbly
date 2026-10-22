const { test } = require("node:test");
const assert = require("node:assert/strict");
const { configuration, deploy } = require("./aws-deploy.cjs");

const root = "arn:aws:ecs:us-east-1:000000000000:";
const digest = "sha256:" + "a".repeat(64);
const secret = "arn:aws:secretsmanager:us-east-1:000000000000:secret:fixture:PRIMARY_DB_DIRECT::version";
function config() {
  return configuration({
    AWS_ECS_CLUSTER: root + "cluster/fixture",
    AWS_MIGRATION_TASK_DEFINITION: root + "task-definition/fixture-migration:2",
    AWS_BACKEND_TASK_DEFINITION: root + "task-definition/fixture-backend:2",
    AWS_CONSUMER_TASK_DEFINITION: root + "task-definition/fixture-consumer:2",
  });
}

function fixture(options = {}) {
  const calls = [];
  const services = ["backend", "consumer"].map((name) => ({
    serviceName: `fixture-${name}`, status: "ACTIVE", desiredCount: 1, runningCount: 1, pendingCount: 0,
    taskDefinition: root + `task-definition/fixture-${name}:1`, deployments: [{ rolloutState: "COMPLETED" }],
    networkConfiguration: { awsvpcConfiguration: { subnets: ["subnet-fixture"], securityGroups: ["sg-fixture"], assignPublicIp: "ENABLED" } },
  }));
  const client = { async send(command) {
    const name = command.constructor.name;
    calls.push({ name, input: command.input });
    if (name === "DescribeTaskDefinitionCommand") {
      const service = /fixture-(\w+):/.exec(command.input.taskDefinition)[1];
      return { taskDefinition: { status: "ACTIVE", containerDefinitions: [{
        name: service, image: `registry/${service === "migration" ? "backend" : service}@${service === "migration" && options.imageMismatch ? "sha256:" + "b".repeat(64) : digest}`,
        entryPoint: service === "migration" ? ["/app/migrate"] : undefined,
        environment: [{ name: "DB_MIGRATIONS_ON_START", value: "false" }],
        secrets: [{ name: service === "migration" ? "PRIMARY_DB" : "PRIMARY_DB_DIRECT", valueFrom: secret }],
      }] } };
    }
    if (name === "DescribeServicesCommand") return { services: services.filter((s) => command.input.services.includes(s.serviceName)).map((s) => structuredClone(s)) };
    if (name === "RunTaskCommand") return options.placementFailure ? { failures: [{ reason: "fixture" }] } : { tasks: [{ taskArn: root + "task/fixture/migration" }] };
    if (name === "DescribeTasksCommand") return { tasks: [{ lastStatus: "STOPPED", stopCode: "EssentialContainerExited", containers: [{ name: "migration", exitCode: options.migrationFailure ? 1 : 0 }] }] };
    if (name === "UpdateServiceCommand") {
      if (command.input.service === "fixture-consumer" && command.input.taskDefinition.endsWith(":2") && options.consumerFailure) throw new Error("consumer update failed");
      const service = services.find((s) => s.serviceName === command.input.service);
      service.taskDefinition = command.input.taskDefinition;
      if (command.input.desiredCount !== undefined) service.runningCount = service.desiredCount = command.input.desiredCount;
      if (options.circuitBreakerRollback && command.input.taskDefinition.endsWith(":2")) service.taskDefinition = root + "task-definition/fixture-backend:1";
      return {};
    }
    throw new Error(`Unexpected command ${name}`);
  } };
  return { client, calls };
}

test("migration success gates both service updates and preserves desired counts", async () => {
  const { client, calls } = fixture();
  await deploy(client, config(), async () => {}, () => {});
  const migration = calls.findIndex((c) => c.name === "DescribeTasksCommand");
  const updates = calls.filter((c) => c.name === "UpdateServiceCommand");
  assert.equal(updates.length, 2);
  assert.ok(calls.indexOf(updates[0]) > migration);
  assert.ok(updates.every((c) => !Object.hasOwn(c.input, "desiredCount")));
  assert.equal(calls.filter((c) => c.name === "RunTaskCommand").length, 1);
});

for (const failure of ["migrationFailure", "placementFailure", "imageMismatch"]) {
  test(`${failure} leaves existing services untouched`, async () => {
    const { client, calls } = fixture({ [failure]: true });
    await assert.rejects(deploy(client, config(), async () => {}, () => {}));
    assert.ok(calls.every((c) => c.name !== "UpdateServiceCommand"));
  });
}

test("consumer rollout failure restores previous service revisions without database rollback", async () => {
  const { client, calls } = fixture({ consumerFailure: true });
  await assert.rejects(deploy(client, config(), async () => {}, () => {}), /consumer update failed/);
  const updates = calls.filter((c) => c.name === "UpdateServiceCommand");
  assert.equal(updates.length, 4);
  assert.ok(updates.slice(2).every((c) => c.input.taskDefinition.endsWith(":1") && c.input.desiredCount === 1));
  assert.equal(calls.filter((c) => c.name === "RunTaskCommand").length, 1);
});

test("a circuit breaker rollback cannot be reported as a successful deployment", async () => {
  const { client } = fixture({ circuitBreakerRollback: true });
  await assert.rejects(deploy(client, config(), async () => {}, () => {}), /rolled back/);
});

test("deployment requires explicit revisions in the selected account and region", () => {
  assert.throws(() => configuration({ AWS_ECS_CLUSTER: root.replace("us-east-1", "eu-west-1") + "cluster/fixture" }));
  const c = config();
  assert.equal(c.counts.backend, undefined);
});
