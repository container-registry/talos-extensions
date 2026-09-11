Talos Linux system extension `datum-connect`, first release (0.0.0-dev.4, 2026-09-11)

A Talos node can now publish local services through a Datum Cloud Application Load Balancer over an outbound-only tunnel, the same way the `cloudflared` extension publishes through Cloudflare Tunnel. No public IP, no inbound firewall rule, no port forward on the router.

## The problem

Talos nodes in home labs, offices, edge sites and private clouds sit behind NAT or a firewall. Anything running on them, an ingress controller, a registry mirror, an internal dashboard, is unreachable from the internet unless somebody opens ports, obtains a public address and terminates TLS at the edge. Until now the only extension covering that gap was `cloudflared`, which ties the setup to a Cloudflare account and Cloudflare's tunnel product.

## What the extension does

The node registers itself as a `Connector` in a Datum Cloud project and keeps an outbound iroh/QUIC tunnel open to Datum's edge. An `HTTPProxy` on the Datum side references that connector as its backend, so requests to the public hostname are proxied through the tunnel to `host:port` on the node. The public side is Datum's Envoy-based load balancer: automatic ACME certificate, generated hostname with custom-hostname support, optional Coraza WAF, basic auth, HTTP/2, gRPC and WebSockets.

The extension bundles three static binaries built from source:

- `datum-connect`, the headless tunnel agent from datum-cloud/connect
- `datumctl`, which the agent uses as its credentials helper
- `datum-connect-service`, a small entrypoint that logs in with a service-account key, finds the node's tunnel by label so restarts keep their hostname, and execs the agent

Configuration is one `ExtensionServiceConfig`: the project id and the tunnel origin as environment variables, the service-account credentials JSON as a config file. State lives in `/var/lib/datum-connect` and survives upgrades.

## What it enables

- Exposing an ingress controller running on the host network of a Talos node, and with it every Ingress behind it, from a box that has no public address.
- Publishing single services directly (`127.0.0.1:port`) without touching Kubernetes at all; the smoke test served the node's kubelet health endpoint.
- Adding more tunnels from the Datum portal or `datumctl apply`; the agent adopts tunnels that reference its connector.
- Running the same workflow on the winti1 worker, a Raspberry Pi, or a QEMU VM on a laptop, since the image is built for amd64 and arm64.

## Benefits over the cloudflared path

- Authentication is a Datum service account with an RSA key that mints its own short-lived JWTs. There is no long-lived tunnel token to rotate and no refresh token that can expire while the node is unattended.
- The public hostname is stable across service restarts, reboots and Talos upgrades. Stopping the service does not delete the tunnel; only an explicit delete does.
- The load balancer side is declarative Kubernetes-style API (`HTTPProxy`, `TrafficProtectionPolicy`, `Connector`), so it can be managed with the same tooling as the cluster.
- WAF, TLS and hostname management come with the load balancer instead of being bolted on per tunnel.
- Everything is built from tagged upstream sources inside the siderolabs extensions build (bldr), with checksums, so the image is reproducible and auditable.

## Verified

End to end on a Talos v1.14.0 arm64 VM: custom installer with the extension, service start, service-account login, connector registration, `https://<hostname>.datumproxy.net/healthz` answering `ok` from the node, and a restart reattaching to the same tunnel and hostname.

## Known limitations

- datum-cloud/connect is at a `v0.0.0-dev` prerelease; the extension pins the tag and checksums.
- Each service restart registers a fresh `Connector` and leaves the previous one for the agent's own cleanup; Datum projects default to a quota of 5 connectors.
- One origin per node configuration. Additional tunnels are created on the Datum side, not in the extension config.
- The extension is not in the Talos Image Factory catalog yet, so the installer has to be built with `imager` and hosted in a registry the node can pull from.
- Creating the service account, its key and the `editor` policy binding is done with `datumctl`; the README carries the exact commands.
