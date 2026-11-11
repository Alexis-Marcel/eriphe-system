# Deploy

Reference deployments of Eriphe's services, for those who run their own Docker Compose project
instead of `install.sh` (see [install/README.md](../install/README.md), the usual way).

## NAS (`nas/compose.yaml`)

An example for a NAS running Docker (paths and the socket's group are Unraid's: `/mnt/user`,
`/mnt/user/appdata`, group 281; change them for another system): eriphe-core and the NAS's
eriphe-node. eriphe-node has no Docker access: it asks `eriphe-engine`
(`ERIPHE_ENGINE_URL=http://eriphe-engine:8792`), the node's own image run as `eriphe-node engine`
(node/engine_server.go, node/README.md, The engine), the only container that holds the Docker
socket. The node sends verbs (run a task, run a check in a task, run a service, stop, a folder
helper, a wake), never a container as such; the engine makes each container itself, with its
hardening, and only answers `eriphe-node`. The engine sits on its own internal network,
`eriphe-docker`, that task containers do not join.

Each task container runs on a network of its own, `eriphe-task-<id>`, which the engine makes for it
(a /28 of 10.213.0.0/16, `-task-pool`) like `eriphe-tasks`: an internal bridge (no route out) with an
isolated gateway (the bridge has no address on the NAS, so the NAS's own services, its web UI
included, are not reachable on it), that only eriphe-node joins besides the task. eriphe-core and
the local MCP services are on `eriphe-system`, which tasks are not on: a task cannot call a service
container directly (bypassing grants, policy and tickets), eriphe-core, nor another task. It reaches
only the node: its task listener on port 8790 and its egress relay on 8791, at the node's address on
its network (the node accepts no other network on them). The relay is the tasks' one way to the Internet, and never dials the LAN
(see node/README.md, Egress).

A request says what is wanted (an image, folders and their access, an environment, limits) and
nothing else: a request with any other field is refused, and none can express `--privileged`, a
capability, a device or GPU, a host namespace, a security option, a network or a volume. The
hardening is the engine's: `--cap-drop ALL` with the three file capabilities a task gets
(`CHOWN`, `DAC_OVERRIDE`, `FOWNER`), `no-new-privileges`, the networks from the engine's own flags
(`-network`, `-task-network`), the node's labels. The host's network is used for one container
only: the node's own image (`-node-image`) sending a wake, read-only, without a mount; the same
image is the only one run as a folder helper. Tasks and services run Eriphe's images only
(`-images`, default `ghcr.io/alexis-marcel/`): add another in `-images` or in a file of
`-image-file`, one a line. A check runs only in the running container of the
task it names; a stop or a removal only touches the node's own containers: the other containers
of the NAS are not the node's.

Folders are only mounted from `/mnt/user/appdata/eriphe-system/node` and `/mnt/user`, the place
whose folders the user may give to Eriphe (`ERIPHE_NODE_SHARES: shares=/mnt/user`, see
node/README.md, Shares) (`-bind-from`). The engine sees both read-only at the same paths, resolves
a source's symlinks and gives the daemon the resolved path, so a link in the node's root does not
lead out of them. Allowing `/mnt/user` widens what whoever controls eriphe-node could mount
(appdata included), which the next section's limit already allows; eriphe-node itself mounts only
the folders the user shared, never appdata nor its own root. Another place to share goes in both
lists (comma-separated) and in the engine's volumes. A refusal answers with its reason, logged
without the request: `docker logs eriphe-engine` says what and why.

### What the engine does not stop

Whoever controls eriphe-node can still do what the node may: run a task container of any image,
with the three file capabilities, the node's root and the shared places mounted (appdata
included, under `/mnt/user`), and run a check in its own tasks. A source is checked when the
container is made and mounted when it starts. Between the two, a task cannot swap what another
container mounts for a symlink: no container mounts a path under a place a task can write while
that task is there (it waits). The node itself writes its own root, though: a compromised node
could still swap a task's folder there for a symlink, a race the engine cannot close.

### From the socket proxy

Before eriphe-node engine, this project ran `docker-proxy` (`eriphe-docker-proxy`) and gave the
node `DOCKER_HOST`. To move to the engine, take this file again (or rename the service to
`engine` with the command above, give the node `ERIPHE_ENGINE_URL` instead of `DOCKER_HOST`, and
`depends_on: [core, engine]`), then `docker compose up -d --remove-orphans`: the proxy's container
goes, the engine starts, the node is made again. The node and the engine must be the same version.
Node images from 0.70 on have no socket proxy (and no docker CLI): with this project not moved yet,
the node connects without a runtime and says so in its log ("no container engine here").

### Updates

This compose project has no updater (install/README.md, The machines update themselves): the
engine above gives the node no new right, and nothing on the NAS applies eriphe-core's update
requests. Settings → Machines → Updates says the NAS cannot update itself and sends it none; set
it to "Jamais" there so that it gives no notice either, and update it by hand:
`docker compose pull && docker compose up -d` in `/mnt/user/appdata/eriphe-system` (eriphe-core
first, then the engine and the node). The images have no tag here, so they are `latest`, the latest release
([RELEASING.md](../RELEASING.md)); write `:edge` on the three of them to follow `main`, or
`:0.2.0` to stay on a release (the node takes the base image and the agents of its own version). It gets an updater, and updates
itself, once it is installed with `install.sh --core-mode` instead, or, on Unraid or any NAS with
Docker, with the one container `ghcr.io/alexis-marcel/eriphe` (install/README.md, One container),
which makes this same stack and updates it.

### Setting it up

Needs Docker 28 or later (`docker version`), for the isolated gateway of `eriphe-tasks`.

1. Check the group of the socket: `stat -c %g /var/run/docker.sock`. If it is not 281, change
   `user:` of `engine`. If you pin the node's image (`:edge`, `:0.2.0`), pin it the same for the
   engine's `image:` and in `-node-image`.
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
5. Check: `docker logs eriphe-node` says it is connected over TLS, with tasks `each on a network
   of its own`; while a task runs, `docker network inspect eriphe-task-<id> --format
   '{{.Internal}} {{.Options}}'` says `true` and `gateway_mode_ipv4:isolated`; a task starts,
   answers and stops, and `docker logs eriphe-engine` shows no refusal; `docker inspect eriphe-node
   --format '{{.Config.Env}}'` shows `ERIPHE_ENGINE_URL` and no `DOCKER_HOST`.
6. Folders: Settings → Folders → "Partager un dossier" offers the machine `nas` and its place
   `shares` (appdata does not show). Lent to read, the task's container has the folder read-only; lent
   to write (Modifier), writable, after a snapshot.
