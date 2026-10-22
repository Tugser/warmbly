# Application runtime

This root reads the foundation state and creates application services in
`us-east-1`. Keep installation-specific inputs outside the checkout. Supply
digest-pinned ARM64 images, valid Fargate sizes, desired task counts, secret key
names and immutable secret version IDs. Secret values never enter Terraform.

```sh
terraform -chdir=deploy/terraform/aws/runtime init \
  -backend-config="bucket=$STATE_BUCKET"
terraform -chdir=deploy/terraform/aws/runtime plan \
  -var-file=/private/path/runtime.tfvars.json \
  -out=/private/path/runtime.tfplan
terraform -chdir=deploy/terraform/aws/runtime apply /private/path/runtime.tfplan
```

Start at desired count zero. Separate execution roles retrieve each service's
configuration and write its logs. Application roles receive reviewed existing
permissions or IAM Policy Autopilot output through `application_policies`.

## Migrate before deployment

Terraform registers the application and migration task definitions. Service
revision and desired-count changes are deployment operations; Terraform ignores
those two fields after service creation. The workflow handles backend and consumer
rollouts. Forms, realtime and tracking cutovers use separate explicit ECS service
updates after their own checks. Start new services at zero and use explicit
counts when activating them. Terraform output task definition ARNs describe
prepared revisions, not necessarily running revisions.

Set `github_repository` to the repository running `.github/workflows/deploy-aws.yml`.
The root creates an OIDC provider and a deployment role scoped to the migration
task and backend/consumer services. Import an existing account-wide GitHub OIDC
provider instead of creating a second one. Restrict the `aws-production` GitHub
environment to trusted deployment branches. Set its variables:

| Variable | Value |
| --- | --- |
| `AWS_DEPLOY_ROLE_ARN` | Terraform `github_deploy_role_arn` output |
| `AWS_ECS_CLUSTER` | Runtime `cluster_arn` output |
| `AWS_ECS_SERVICE_PREFIX` | Runtime `name` input |

Prepare a backend secret version with `PRIMARY_DB_DIRECT`, pointing to port 5432.
The migration task injects only that key, with no application task role. The
workflow never retrieves database credentials. Its backend and migration tasks
must use the same immutable image digest and direct secret reference. Keep the
default `migration_secret_key=PRIMARY_DB_DIRECT` for workflow deployments.

Dispatch the workflow with explicit Terraform task definition revisions for
migration, backend and consumer. It runs `/app/migrate` inside the VPC over the
existing PrivateLink route. An unsuccessful or unresolved migration blocks all
service updates. A successful migration permits sequential backend and consumer
rollouts, verifying that the requested revision becomes stable. A rollout failure
restores the previous service revisions and counts, retaining the current schema.
Inspect rollback health separately. The workflow never runs down migrations.
Task definitions use `skip_destroy` so earlier revisions remain active for
rollback. Retain their referenced images and secret versions throughout the
rollback window; retire them separately after it closes.

The runtime sets `DB_MIGRATIONS_ON_START=false` for the backend. New application
images must support that setting and schema readiness before activation. Keep
migrations additive and compatible with the previous release throughout rollout
and rollback. Do not interrupt an active migration; if a workflow is cancelled or
times out, inspect the ECS task before rerunning it.

Run `database_check_task_definition` to exercise the backend image's actual Go
client against its configured pooler without starting the API or background jobs.
It collects operator status into temporary task storage and logs only completion.
The separate dependency validation task checks provider connectivity and stored
credential decryption. Neither check activates the backend service.

Tasks use public IPs for outbound access, avoiding NAT gateway charges. Inbound
application traffic is restricted to the HTTPS load balancer. The backend also
accepts private task traffic through service discovery. Restrict the load
balancer to approved caller CIDRs, such as Cloudflare's published ranges. Public
DNS changes are separate from deploying the services.

Run `validation_task_definition` once in a public subnet with a public IP and the
task security group. It checks private database DNS and logins, stored mailbox
and MFA decryption, KMS, both caches, Kafka metadata, Schema Registry and S3 using
the actual backend task role. It writes no production data, logs only counts and
results, and exits. Observe its stopped exit code and logs before activation.
The pinned validation packages are installed in temporary task storage.

Keep one cache authoritative throughout deployment overlap. A backend starts
background jobs immediately; its desired count is the
activation control. Preserve compatible images, encryption material and shared
coordination while the previous deployment drains. Verify target health and
HTTPS routes before switching DNS. New injected values require replacing tasks.
