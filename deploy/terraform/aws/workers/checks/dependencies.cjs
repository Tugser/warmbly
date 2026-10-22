const https = require("node:https");
const { Kafka, logLevel } = require("kafkajs");
const { createClient } = require("redis");

const results = [];
const timer = setTimeout(() => process.exit(1), 120000);
async function check(name, fn) {
  try { await fn(); results.push(true); console.log(JSON.stringify({ name, ok: true })); }
  catch { results.push(false); console.log(JSON.stringify({ name, ok: false })); }
}
async function main() {
  await check("shared_cache", async () => {
    const client = createClient({ url: process.env.REDIS, socket: { connectTimeout: 10000, reconnectStrategy: false } });
    client.on("error", () => {});
    try { await client.connect(); if (await client.ping() !== "PONG") throw new Error("Cache check failed"); }
    finally { client.destroy(); }
  });
  await check("private_cache_tls", async () => {
    if (!process.env.NEW_REDIS_PASSWORD) throw new Error("Replacement cache credential required");
    const client = createClient({ password: process.env.NEW_REDIS_PASSWORD, socket: { host: process.env.CACHE_HOST, port: 6379, tls: true, servername: process.env.CACHE_HOST, rejectUnauthorized: true, connectTimeout: 10000, reconnectStrategy: false } });
    client.on("error", () => {});
    try { await client.connect(); if (await client.ping() !== "PONG") throw new Error("Cache check failed"); }
    finally { client.destroy(); }
  });
  await check("replacement_kafka_key", async () => {
    const kafka = new Kafka({ clientId: "worker-migration-validation", brokers: process.env.KAFKA_BOOTSTRAP_SERVERS.split(","), ssl: true, sasl: { mechanism: "plain", username: process.env.KAFKA_SASL_USERNAME, password: process.env.KAFKA_SASL_PASSWORD }, logLevel: logLevel.NOTHING, connectionTimeout: 10000, requestTimeout: 15000, retry: { retries: 1 } });
    const admin = kafka.admin({ allowAutoTopicCreation: false });
    try { await admin.connect(); await admin.fetchTopicMetadata({ topics: ["tracking-events"] }); }
    finally { await admin.disconnect(); }
  });
  await check("broker_authorization", () => new Promise((resolve, reject) => {
    const token = process.env.NODE_BROKER_TOKEN || process.env.ENCRYPTED_KEYS_WORKER_TOKEN || process.env.INTERNAL_API_TOKEN;
    const url = new URL("/api/v1/internal/dek/00000000-0000-0000-0000-000000000000", process.env.ENCRYPTED_KEYS_BACKEND_URL);
    const req = https.get(url, { headers: { authorization: "Bearer " + token }, timeout: 15000 }, (res) => { res.resume(); res.on("end", () => [200, 404].includes(res.statusCode) ? resolve() : reject(new Error("Broker refused request"))); });
    req.on("timeout", () => req.destroy());
    req.on("error", reject);
  }));
  clearTimeout(timer);
  console.log(JSON.stringify({ checks: results.length, passed: results.filter(Boolean).length, worker_started: false }));
  process.exit(results.every(Boolean) ? 0 : 1);
}
main().catch(() => process.exit(1));
