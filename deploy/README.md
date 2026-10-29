# Deploy

Reference deployments of Eriphe's services, for those who run their own Docker Compose project
instead of `install.sh` (see [install/README.md](../install/README.md), the usual way).

## NAS (`nas/compose.yaml`)

An example for a NAS running Docker (paths and the socket's group are Unraid's: `/mnt/user`,
`/mnt/user/appdata`, group 281; change them for another system): eriphe-core and the NAS's
eriphe-node. eriphe-node has no Docker socket: it talks to Docker through
[wollomatic/socket-proxy](https://github.com/wollomatic/socket-proxy) (`DOCKER_HOST=tcp://eriphe-docker-proxy:2375`),
which only forwards the calls the node makes and only accepts connections from `eriphe-node`. The
proxy sits on its own internal network, `eriphe-docker`, that task containers do not join.

Task containers run on `eriphe-tasks`, an internal bridge (no route out) that only eriphe-node
joins besides them, with an isolated gateway (the bridge has no address on the NAS, so the NAS's
own services, its web UI included, are not reachable on it). eriphe-core and the local MCP services
are on `eriphe-system`, which tasks are not on: a task cannot call a service container directly
(bypassing grants, policy and tickets) nor eriphe-core. It reaches only the node: its task listener
as `eriphe-node:8790` and its egress relay as `eriphe-node:8791` (both bound to the node's address
on `eriphe-tasks` only). The relay is the tasks' one way to the Internet, and never dials the LAN
(see node/README.md, Egress).

| Allowed | For |
|---|---|
| `HEAD/GET /_ping`, `GET /version` | the CLI's handshake, `docker version` |
| `GET /networks/bridge`, `GET /networks/eriphe-tasks` | the gateways and subnet of the networks, for `host.docker.internal` and the task listener's address |
| `GET /containers/json`, `GET /images/json` | `docker ps -a --filter`, `docker images` |
| `POST /containers/create`, `POST /images/create` | `docker run` (and its image pull) |
| `POST /containers/{id}/attach`, `/start`, `/wait`, `/stop` | `docker run -i` (hijacked stdin/stdout stream), `docker stop` |
| `DELETE /containers/{id}` | `docker rm -f` |

Bind mounts are only accepted from `/mnt/user/appdata/eriphe-system/node` and `/mnt/user`, the
place whose folders the user may give to Eriphe (`ERIPHE_NODE_SHARES: shares=/mnt/user`, see
node/README.md, Shares) (`-allowbindmountfrom`); named volumes such as `eriphe-cache` are not bind
mounts and stay allowed. Allowing `/mnt/user` widens what whoever controls eriphe-node could mount
(appdata included), which the next section's limit already allows; eriphe-node itself mounts only
the folders the user shared, never appdata nor its own root. Another place to share goes in both
lists (comma-separated). Everything else is
refused with 403: `exec`, `kill`, `cp` (archive), `inspect` of a container (it would show its
environment), volumes, networks, `info`, image removal, builds.

### What the proxy does not stop

The proxy reads a container's mounts, not the rest of its configuration. Whoever controls
eriphe-node (or the proxy's port) can still create a container that is `--privileged`, adds
capabilities or devices, or uses the host's PID or network namespace, which amounts to root on
the NAS. They can also attach to, stop or remove other containers, since a container ID does not
say whose it is. eriphe-node itself only ever builds the `docker run` of `node/container.go`
(fixed hardening flags, mounts refused outside its root), so this is the risk of eriphe-node
being taken over, not of a task. Closing it needs a filter on the whole create request (a Docker
authorization plugin, or a proxy that checks `HostConfig`).

### Updates

This compose project has no updater (install/README.md, The machines update themselves): the
proxy above gives the node no new right, and nothing on the NAS applies eriphe-core's update
requests. Settings → Machines → Updates says the NAS cannot update itself and sends it none; set
it to "Jamais" there so that it gives no notice either, and update it by hand:
`docker compose pull && docker compose up -d` in `/mnt/user/appdata/eriphe-system` (eriphe-core
first, then the node). The images have no tag here, so they are `latest`, the latest release
([RELEASING.md](../RELEASING.md)); write `:edge` on the three of them to follow `main`, or
`:0.2.0` to stay on a release (the node takes the base image and the agents of its own version). It gets an updater, and updates
itself, once it is installed with `install.sh --core-mode` instead (Unraid has no systemd: not
yet).

### Setting it up

Needs Docker 28 or later (`docker version`), for the isolated gateway of `eriphe-tasks`.

1. Check the group of the socket: `stat -c %g /var/run/docker.sock`. If it is not 281, change
   `user:` of `docker-proxy`.
2. Copy `nas/compose.yaml` to `/mnt/user/appdata/eriphe-system/compose.yaml`, with a `core.json`
   next to it (see core/README.md, Configuration file: give it a `web` section, or there is no
   workspace) and a `secrets/` folder for the files it names. `core/` holds eriphe-core's data:
   keep it, with its certificate authority (`machine-ca.pem`, `machine-ca-key.pem`), out of
   backups that leave home.
3. `docker compose up -d core`, then get a join token for the NAS's node:
   `docker exec eriphe-core eriphe-core join-token -name nas -quiet`.
4. `ERIPHE_JOIN_TOKEN=<token> docker compose up -d`. Optionally set `ERIPHE_CA_HASH` (the hash from
   Settings → Machines) so that the node checks eriphe-core's CA instead of trusting the first
   exchange.
5. Check: `docker logs eriphe-node` says it is connected over TLS, with `tasks on eriphe-tasks`
   and an `egress relay on 172.x.y.z:8791`; `docker network inspect eriphe-tasks --format
   '{{.Internal}} {{.Options}}'` says `true` and `gateway_mode_ipv4:isolated`; a task starts,
   answers and stops, and `docker logs eriphe-docker-proxy` shows no blocked request.
6. Folders: Settings → Folders → "Partager un dossier" offers the machine `nas` and its place
   `shares` (appdata does not show). With "Lecture et dépôt", the task's container has the folder
   read-only and its `Eriphe/` subfolder writable.
