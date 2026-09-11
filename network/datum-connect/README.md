# Datum Connect

Datum Connect registers a Talos node as a [Connector](https://www.datum.net/docs/connectors/tunnels) in a
Datum Cloud project and serves outbound-only iroh/QUIC tunnels. Services on the node (an ingress
controller on the host network, a NodePort, anything the node can reach) are then published through a
Datum [Application Load Balancer](https://www.datum.net/docs/alb/overview) without a public IP or inbound
firewall rules, the same way `cloudflared` publishes through Cloudflare Tunnel.

The extension bundles three binaries from upstream sources:

- `datum-connect`, the headless tunnel agent from [datum-cloud/connect](https://github.com/datum-cloud/connect)
  (built static-musl with a rustup-pinned toolchain; the upstream release binary is glibc-dynamic)
- `datumctl` from [datum-cloud/datumctl](https://github.com/datum-cloud/datumctl), which the agent uses as its
  credentials helper (`datumctl auth get-token --session ...`)
- `datum-connect-service`, a small entrypoint (`entrypoint/main.go`) that logs `datumctl` in with the
  mounted service-account credentials, finds the tunnel for this node's label so restarts keep their
  hostname, and execs `datum-connect listen`

## Installation

The extension is not in the Image Factory catalog. Build an installer with `imager` and upgrade the node:

```bash
docker run --rm -t -v $PWD/_out:/out ghcr.io/siderolabs/imager:v1.14.0 installer --arch amd64 \
  --system-extension-image ghcr.io/siderolabs/datum-connect:<version>@sha256:<digest>
crane push _out/installer-amd64.tar <registry>/talos-installer:v1.14.0-datum-connect
talosctl upgrade --image <registry>/talos-installer:v1.14.0-datum-connect --preserve
```

## Usage

1. Create a service account in the Datum project, a key for it, and an `editor` binding on the project.
   The key's `status.privateKey` is the credentials JSON file datumctl accepts (same as the portal download):

   ```bash
   datumctl ctx use <org>/<project>
   datumctl create -f - <<'EOF'
   apiVersion: iam.miloapis.com/v1alpha1
   kind: ServiceAccount
   metadata: {name: talos-node}
   spec: {state: Active}
   EOF
   datumctl create -f - --validate=false -o jsonpath='{.status.privateKey}' <<'EOF' > credentials.json
   apiVersion: identity.miloapis.com/v1alpha1
   kind: ServiceAccountKey
   metadata: {name: talos-node-key1}
   spec: {serviceAccountUserName: talos-node@<project>.identity.miloapis.com}
   EOF
   datumctl create -f - --validate=false --organization <org> -n organization-<org> <<'EOF'
   apiVersion: iam.miloapis.com/v1alpha1
   kind: PolicyBinding
   metadata: {name: project-<project>-talos-node}
   spec:
     resourceSelector:
       resourceRef: {apiGroup: resourcemanager.miloapis.com, kind: Project, name: <project>, uid: <project uid>}
     roleRef: {name: editor, namespace: datum-cloud}
     subjects:
       - {kind: ServiceAccount, name: talos-node, uid: <service account uid>}
   EOF
   ```

2. Configure the service. The credentials file is mounted read-only; the session, endpoint key and tunnel
   state live in `/var/lib/datum-connect`.

   ```yaml
   # datum-connect-config.yaml
   ---
   apiVersion: v1alpha1
   kind: ExtensionServiceConfig
   name: datum-connect
   environment:
     - DATUM_PROJECT=project-abc12
     - DATUM_CONNECT_TUNNEL_ORIGIN=127.0.0.1:80
     # optional, defaults to the node hostname; the tunnel is looked up by this label on restart
     - DATUM_CONNECT_TUNNEL_LABEL=mynode-ingress
   configFiles:
     - mountPath: /usr/local/etc/datum-connect/credentials.json
       content: |
         <contents of credentials.json>
   ```

   ```bash
   talosctl patch mc -p @datum-connect-config.yaml
   talosctl logs ext-datum-connect
   ```

   The log ends with `Tunnel ready: https://<words>.datumproxy.net`; the hostname is also in
   `datumctl get httpproxies -o wide`.

Environment variables read by the entrypoint and agent:

| Variable | Purpose |
| --- | --- |
| `DATUM_PROJECT` | project the Connector and tunnel are created in (required) |
| `DATUM_CONNECT_TUNNEL_ORIGIN` | `host:port` the tunnel forwards to (required for the first start) |
| `DATUM_CONNECT_TUNNEL_LABEL` | tunnel display name, default node hostname |
| `DATUM_CREDENTIALS_FILE` | credentials path, default `/usr/local/etc/datum-connect/credentials.json` |
| `DATUM_CONNECT_RELAY_URLS` | override the iroh relay list |
| `DATUM_API_ENV` | `staging` to target the staging API |
| `RUST_LOG` | log filter, default `info` |

Stopping the service does not delete the tunnel (only an interactive Ctrl+C does), so the hostname is
stable across restarts and upgrades. Delete it with `datumctl delete httpproxy <tunnel-id>` or the portal.
Wiping `/var/lib/datum-connect` gives the node a new Connector identity; the agent cleans up its own
orphaned Connectors on the next start.
