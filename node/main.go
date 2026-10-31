// eriphe-node runs Eriphe's tasks on one machine, like a kubelet: every task
// is a container started from a worker image, in which the agent works.
// eriphe-node relays its progress, questions and permission requests.
//
// It dials eriphe-core and needs no open port. The first run joins with a
// token from `eriphe-core join-token`; later runs reuse the saved secret and
// certificate.
//
//	eriphe-node -core https://nas:8800 -join-token <token> -name pc -ca-hash sha256:<hex>
//	eriphe-node
package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// version is set at build time (the image: X.Y.Z for a release,
// X.Y.Z-dev+<commit> for a build of main, see core/README.md, Versions),
// and reported to eriphe-core with what this machine offers.
var version = "dev"

type options struct {
	root        string // where tasks and profiles live, as seen by eriphe-node
	hostRoot    string // the same directory as seen by the Docker daemon
	image       string // default worker image
	agentImage  string // image of each agent package, {agent} for its name (inject.go); empty: none injected
	taskBin     string // the node's own programs for tasks, by platform (inject.go)
	network     string // Docker network of services with egress, shared with eriphe-node (and eriphe-core on the NAS)
	taskNetwork string // Docker network of task containers, of their own
	cacheVolume string // Docker volume mounted at /cache in task containers
	callbackURL string // how task containers reach eriphe-node
	listenAddr  string // where the task listener is bound (set at start)
	egressPort  string // port of the egress relay, on the listener's host (empty: none)
	docker      string // docker CLI, or Apple's container CLI with that engine
	engine      string // what runs the tasks: docker, or container (Apple's, on a Mac: apple.go)
	// With the container engine: what the node downloads at most, in bytes,
	// of a task's folders kept in the cloud (iCloud Drive), before it starts.
	downloadLimit int64
	labels        []string          // what the scheduler can ask for, e.g. gpu
	install       string            // how the installers installed it: linux, macos or windows; empty by hand
	alwaysOn      bool              // the machine never sleeps: eriphe-core puts the tools (services) here
	wake          wakeInfo          // how eriphe-core can wake this machine (wake.go)
	volumes       map[string]string // named host folders offered to tasks, by name
	shares        shareFlag         // roots the user may share folders from, by name (shares.go)
	taskMemory    string            // memory limit of a task container (docker --memory), empty: none
	taskCPUs      string            // CPU limit of a task container (docker --cpus), empty: none
	// Services (local MCP servers).
	serviceMemory  string // memory limit of a service container, empty: none
	serviceNetwork string // internal Docker network for http services without egress, empty: none
	// A model server on this machine (Ollama, llama.cpp, vLLM, LM Studio)
	// that eriphe-core may reach through the tunnel: its root address, its
	// /v1 API under it (model.go). Empty: none.
	modelServer string
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "core-relay" {
		runCoreRelay(os.Args[2:]) // a Mac's LaunchDaemon (corerelay.go)
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "wake-send" {
		runWakeSend(os.Args[2:]) // the short-lived container that wakes a machine (wake.go)
		return
	}
	core := flag.String("core", os.Getenv("ERIPHE_CORE"), "eriphe-core URL, or several separated by commas, preferred first, e.g. the local one then the remote one (env ERIPHE_CORE)")
	joinToken := flag.String("join-token", os.Getenv("ERIPHE_JOIN_TOKEN"), "join token, for the first run (env ERIPHE_JOIN_TOKEN)")
	caHashFlag := flag.String("ca-hash", os.Getenv("ERIPHE_CA_HASH"), "sha256:<hex> of eriphe-core's certificate authority, as join-token prints it: the node then reaches eriphe-core over TLS only, checked against it (env ERIPHE_CA_HASH)")
	name := flag.String("name", envOr("ERIPHE_NODE_NAME", defaultName()), "node name, for the first run (env ERIPHE_NODE_NAME)")
	listen := flag.String("listen", ":8790", "address task containers reach this node on; without a host, this node's address on the task network")
	egressListen := flag.String("egress-listen", ":8791", "address of the egress relay, the tasks' only way out of their internal network; without a host, the -listen one (empty: none, tasks reach only this node)")
	var o options
	flag.StringVar(&o.root, "root", envOr("ERIPHE_ROOT", "data"), "directory for the node's state and task profiles (env ERIPHE_ROOT)")
	flag.StringVar(&o.hostRoot, "host-root", "", "the -root directory as the Docker daemon sees it (default: same path)")
	flag.StringVar(&o.image, "image", "ghcr.io/alexis-marcel/eriphe-base:"+imageTag(version), "default worker image, the one tasks start from (the agent is injected)")
	flag.StringVar(&o.agentImage, "agent-image", envOr("ERIPHE_AGENT_IMAGE", "ghcr.io/alexis-marcel/eriphe-agent-{agent}:"+imageTag(version)), "image of each agent injected into tasks, {agent} for claude, codex or opencode (env ERIPHE_AGENT_IMAGE)")
	flag.StringVar(&o.taskBin, "task-bin", envOr("ERIPHE_TASK_BIN", "/usr/local/share/eriphe/task-bin"), "the node's programs for tasks: linux-<arch>/ in it is copied into every task's /opt/eriphe/bin (env ERIPHE_TASK_BIN)")
	flag.StringVar(&o.network, "network", "", "Docker network for services with egress, shared with this node (empty: Docker's default bridge)")
	flag.StringVar(&o.taskNetwork, "task-network", "eriphe-tasks", "Docker network for task containers, apart from eriphe-core and the services (created if missing)")
	flag.StringVar(&o.cacheVolume, "cache-volume", "eriphe-cache", "Docker volume for the tasks' tool and package caches (empty: none)")
	flag.StringVar(&o.callbackURL, "callback-url", "http://host.docker.internal:8790", "URL task containers use to reach this node")
	flag.StringVar(&o.docker, "docker", "", "the engine's CLI (default: docker, or container with -engine container)")
	flag.StringVar(&o.engine, "engine", envOr("ERIPHE_ENGINE", engineDocker), "what runs the tasks: docker, or container (Apple's container, on a Mac with Apple silicon and macOS 26: a micro-VM per task) (env ERIPHE_ENGINE)")
	downloadLimit := flag.String("download-limit", "2g", "with -engine container: what the node downloads at most of a task's folders kept in the cloud (iCloud Drive) before it starts, e.g. 2g")
	flag.StringVar(&o.taskMemory, "task-memory", "8g", "memory limit of each task container, e.g. 8g (empty: none)")
	flag.StringVar(&o.taskCPUs, "task-cpus", "", "CPU limit of each task container, e.g. 4 (empty: all the machine's CPUs)")
	flag.StringVar(&o.serviceMemory, "service-memory", "1g", "memory limit of each service container (empty: none)")
	flag.StringVar(&o.serviceNetwork, "service-network", "", "internal Docker network, shared with the node, for http services without egress")
	flag.BoolVar(&o.alwaysOn, "always-on", os.Getenv("ERIPHE_ALWAYS_ON") == "1", "this machine stays on, like the one eriphe-core runs on: tools go here first (env ERIPHE_ALWAYS_ON=1)")
	// From install.sh and install.ps1, or guessed (install.go): eriphe-core shows the command that updates this machine.
	o.install = installKind(thisHost())
	wakeMAC := flag.String("wake-mac", os.Getenv("ERIPHE_WAKE_MAC"), "MAC address(es) of this machine's network card, comma-separated, so that eriphe-core can wake it (Wake-on-LAN); default: the card it reaches eriphe-core through, when the node runs on the machine itself (env ERIPHE_WAKE_MAC)")
	wakeBroadcast := flag.String("wake-broadcast", os.Getenv("ERIPHE_WAKE_BROADCAST"), "broadcast address of that card's network, e.g. 192.0.2.255 (env ERIPHE_WAKE_BROADCAST)")
	modelServer := flag.String("model-server", os.Getenv("ERIPHE_MODEL_SERVER"), "a model server this node may lend to Eriphe through its tunnel (Ollama, llama.cpp, vLLM, LM Studio), as this node reaches it, e.g. http://127.0.0.1:11434; only its OpenAI-compatible /v1 API is reached, and no port is opened (env ERIPHE_MODEL_SERVER)")
	labels := flag.String("labels", os.Getenv("ERIPHE_NODE_LABELS"), "comma-separated labels agents can require, e.g. gpu (env ERIPHE_NODE_LABELS)")
	o.volumes = volumeFlag{}
	if err := volumeFlag(o.volumes).Set(os.Getenv("ERIPHE_NODE_VOLUMES")); err != nil {
		log.Fatal(err)
	}
	o.shares = shareFlag{}
	if err := o.shares.Set(os.Getenv("ERIPHE_NODE_SHARES")); err != nil {
		log.Fatal(err)
	}
	flag.Var(o.shares, "share", "root the user may share folders from, name=/path, or name=/path:ro where results may not be dropped; the same path for this node and the Docker daemon; repeatable (env ERIPHE_NODE_SHARES: name=path,name2=path2:ro)")
	flag.Var(volumeFlag(o.volumes), "volume", "host folder offered to tasks, name=/host/path, repeatable (env ERIPHE_NODE_VOLUMES: name=path,name2=path2)")
	flag.Parse()
	for _, l := range strings.Split(*labels, ",") {
		if l = strings.TrimSpace(l); l != "" {
			o.labels = append(o.labels, l)
		}
	}

	var err error
	if o.modelServer, err = parseModelServer(*modelServer); err != nil {
		log.Fatal(err)
	}
	switch o.engine {
	case engineDocker, engineContainer:
	default:
		log.Fatalf("-engine %q: docker or container", o.engine)
	}
	if o.engine == engineContainer && runtime.GOOS != "darwin" {
		log.Fatal("-engine container is Apple's container, on a Mac: use docker here")
	}
	if o.docker == "" {
		o.docker = o.engine
	}
	if o.downloadLimit, err = parseBytes(*downloadLimit); err != nil {
		log.Fatalf("-download-limit: %v", err)
	}
	if o.root, err = filepath.Abs(o.root); err != nil {
		log.Fatal(err)
	}
	if o.hostRoot == "" {
		o.hostRoot = o.root
	}
	for _, d := range []string{"profiles", "tasks", "services", "agents"} {
		if err := os.MkdirAll(filepath.Join(o.root, d), 0o700); err != nil {
			log.Fatal(err)
		}
	}

	pin, err := parseCAHash(*caHashFlag)
	if err != nil {
		log.Fatal(err)
	}
	statePath := filepath.Join(o.root, "node.json")
	s, err := loadState(statePath)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if *core == "" || *joinToken == "" {
			log.Fatal("this node has not joined yet: give -core and -join-token (from `eriphe-core join-token`)")
		}
		if s, err = join(statePath, parseCores(*core), *name, *joinToken, pin); err != nil {
			log.Fatal(err)
		}
		log.Printf("joined %s as %s", s.urls()[0], s.Name)
	case err != nil:
		log.Fatal(err)
	default:
		s.path = statePath
		if err := s.loadTLS(pin); err != nil {
			log.Fatal(err)
		}
		if s.setURLs(parseCores(*core)) {
			// Given again, the addresses replace the saved ones (e.g. to add the remote one).
			if err := s.save(statePath); err != nil {
				log.Fatal(err)
			}
			log.Printf("eriphe-core addresses: %s", strings.Join(s.urls(), ", "))
		}
	}
	go s.renewLoop()
	if o.wake, err = wakeSettings(thisHost(), *wakeMAC, *wakeBroadcast, s.urls()); err != nil {
		log.Fatal(err)
	}

	if err := checkNetworks(o); err != nil {
		log.Fatal(err)
	}
	n := newNode(o)
	if err := n.ensureTaskNetwork(); err != nil {
		log.Fatal(err)
	}
	addr, err := n.listenAddr(*listen)
	if err != nil {
		log.Fatal(err)
	}
	n.opt.listenAddr = addr
	var egressAddr string
	if *egressListen != "" {
		if egressAddr, err = egressListenAddr(*egressListen, addr); err != nil {
			log.Fatal(err)
		}
		_, n.opt.egressPort, _ = net.SplitHostPort(egressAddr)
		var relay *egressProxy
		if n.apple() {
			// The listener answers on the loopback too; a task names it by
			// its network's gateway.
			_, port, _ := net.SplitHostPort(addr)
			relay = newEgressProxy(n.egressAuthorizer, nil, net.JoinHostPort("127.0.0.1", port))
			relay.isSelf = func(hp string) bool { return n.nets.gateway(hp, port) }
		} else {
			relay = newEgressProxy(n.egressAuthorizer, selfTargets(o.callbackURL, addr), addr)
		}
		l, err := n.listenTasks(egressAddr)
		if err != nil {
			log.Fatal(err)
		}
		go func() { log.Fatal(http.Serve(l, relay)) }()
	}
	api := http.NewServeMux()
	n.routes(api)
	go serve(s, api)
	go n.sync(s)
	go n.prepareLoop()

	// Only task containers call this listener, each with its own secret:
	// their permission endpoint, the relay to their catalog servers, and
	// their credentials on demand (credential.go).
	perm := http.NewServeMux()
	perm.Handle("/perm/{id}/mcp", n.permHandler())
	perm.Handle("/mcp/{server}", n.relayHandler(s))
	perm.Handle("/model/v1/{path...}", n.modelRelayHandler(s))
	perm.Handle("POST "+credentialPath, n.credentialHandler(s))
	for _, sh := range o.shareList() {
		if sh.Missing {
			log.Printf("share %s: %s is not there now", sh.Name, o.shares[sh.Name].Path)
		}
	}
	l, err := n.listenTasks(addr)
	if err != nil {
		log.Fatal(err)
	}
	if n.apple() {
		log.Printf("eriphe-node %s (%s), image %s, tasks with Apple's container, each on a network of its own, task listener on %s, egress relay on %s (tasks' networks only)",
			s.Name, version, o.image, addr, orNone(egressAddr))
	} else {
		log.Printf("eriphe-node %s (%s), image %s, tasks on %s, services on %s, task listener on %s, egress relay on %s",
			s.Name, version, o.image, dockerNetwork(o.taskNetwork), dockerNetwork(o.network), addr, orNone(egressAddr))
	}
	log.Fatal(http.Serve(l, perm))
}

// listenTasks listens for the task containers; with the container engine,
// on every address but for the tasks' networks and the loopback only.
func (n *node) listenTasks(addr string) (net.Listener, error) {
	l, err := net.Listen("tcp", addr)
	if err != nil || !n.apple() {
		return l, err
	}
	return taskListener{l, &n.nets}, nil
}

// parseBytes reads a size such as 2g, 500m or 1024 (bytes).
func parseBytes(s string) (int64, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	mult := int64(1)
	for suffix, m := range map[string]int64{"k": 1 << 10, "m": 1 << 20, "g": 1 << 30, "t": 1 << 40} {
		if v, ok := strings.CutSuffix(s, suffix); ok {
			s, mult = v, m
			break
		}
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v < 0 {
		return 0, fmt.Errorf("%q: a size such as 2g or 500m", s)
	}
	return v * mult, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}
