# EC2 worker hosts

This root prepares one ARM `t4g.small` per worker in `us-east-1`, each with an
encrypted 16 GiB gp3 disk, static IPv4 address and pinned worker image. Standard
CPU credits cap burst charges; depleted credits throttle to baseline. Monitor
credit balance and fleet health before changing size or credit mode.

```sh
terraform -chdir=deploy/terraform/aws/workers init \
  -backend-config="bucket=$STATE_BUCKET"
terraform -chdir=deploy/terraform/aws/workers plan \
  -var-file=/private/path/workers.tfvars.json \
  -out=/private/path/workers.tfplan
terraform -chdir=deploy/terraform/aws/workers apply /private/path/workers.tfplan
```

Supply the SSH public key, operator CIDRs, cache security group ID and worker
image metadata through private inputs. SSH is restricted to those CIDRs, and
IMDSv2 is required. Hosts have no instance profile or application AWS
credentials. Workers use brokered KMS and blob operations through the backend.

Bootstrap installs Docker and prepares systemd without starting the worker. An
operator delivers configuration from the worker's Secrets Manager container
over SSH into `/etc/warmbly/node.env`, owned by root with mode `0600`. Credentials
never enter user data, Terraform or command arguments. Preserve worker IDs,
mailbox encryption keys, OAuth identities and compatible broker configuration.
Package installation and image download retry while the static IP association
becomes usable during first boot.

Never run both hosts with the same worker ID. Drain and stop the previous
process, confirm it exited, start the replacement, and verify fresh heartbeats,
command consumption and mailbox health before advancing to the next host. The
prepared service allows 120 seconds for Docker shutdown. Preserve the previous
host for rollback. Static AWS IPs remain stable, but differ from the previous
provider's addresses, so verify mailbox authentication during the handover.

Private Valkey access is allowed from the worker security group. Coordinate its
activation with every control-plane client, and check configured mail ports and
AWS outbound restrictions before moving work. Do not switch one worker to an
empty independent cache.

The dependency check expects `CACHE_HOST` and `NEW_REDIS_PASSWORD` in addition to
the worker environment. Supply the replacement password through protected
temporary input. It verifies both TLS certificate validation and an authenticated
`PING` against private Valkey, without changing the active cache endpoint.

Instances and addresses have destruction guards. AMI and bootstrap drift do not
silently replace a running worker. Roll out host upgrades deliberately while
preserving worker identity and static addresses.
