package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
)

// "A new version is available": every hour, eriphe-core asks the
// registry, anonymously, which version the images' `latest` tag is (the
// latest release), and compares it with its own and each node's. The images
// carry their version as a label (org.opencontainers.image.version, from
// the Dockerfiles), the same as the binaries' (see the README's
// "Versions"): the manifest of `latest` leads to the image's configuration,
// which holds it. A digest would say that `latest` changed, not which
// version it is, and eriphe-core does not know the digest of the image it
// runs from. A build is behind when it is a release older than latest's
// (semver), or a build from before the releases (sha-<commit>); a build of
// main (X.Y.Z-dev+<commit>) or a local one (dev) never is (version.go). The
// registry unreachable, nothing is said: the last answer stays, and the
// error is logged once. Settings → Devices may also check now, once a
// minute at most (checkNow).

const (
	registryURL    = "https://ghcr.io"
	registryOwner  = "alexis-marcel"
	versionLabel   = "org.opencontainers.image.version"
	updateEvery    = time.Hour
	checkNowEvery  = time.Minute // a check asked from the workspace, at most
	updateTimeout  = 10 * time.Second
	maxRegistryDoc = 1 << 20
)

// The images whose latest version is checked.
var updateImages = []string{"eriphe-core", "eriphe-node"}

type updates struct {
	base   string // the registry, replaced in tests
	client *http.Client

	mu     sync.Mutex
	latest map[string]string // image → the version of its latest tag
	failed bool              // the last check failed (logged once)
	at     time.Time         // when the last check ended
	asked  time.Time         // when the workspace last asked for one (checkNow)
}

func newUpdates(base string) *updates {
	return &updates{base: strings.TrimSuffix(base, "/"), client: &http.Client{Timeout: updateTimeout},
		latest: map[string]string{}}
}

// run checks at start, without holding it up, then every hour.
func (u *updates) run(ctx context.Context) {
	u.check(ctx)
	t := time.NewTicker(updateEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			u.check(ctx)
		}
	}
}

func (u *updates) check(ctx context.Context) {
	var errs []error
	for _, image := range updateImages {
		ctx, cancel := context.WithTimeout(ctx, updateTimeout)
		v, err := u.latestVersion(ctx, image)
		cancel()
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", image, err))
			continue
		}
		u.mu.Lock()
		u.latest[image] = v
		u.mu.Unlock()
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if len(errs) > 0 && !u.failed {
		log.Printf("updates: cannot read the latest versions (not logged again until a check works): %v", errors.Join(errs...))
	}
	u.failed = len(errs) > 0
	u.at = time.Now()
}

// checkNow checks at once, unless the workspace asked less than a minute
// ago: false then, and nothing is asked of the registry.
func (u *updates) checkNow(ctx context.Context) bool {
	u.mu.Lock()
	if !u.asked.IsZero() && time.Since(u.asked) < checkNowEvery {
		u.mu.Unlock()
		return false
	}
	u.asked = time.Now()
	u.mu.Unlock()
	u.check(ctx)
	return true
}

// checked is when the last check ended, zero before any.
func (u *updates) checked() time.Time {
	if u == nil {
		return time.Time{}
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.at
}

// version is the latest version of an image, empty while unknown.
func (u *updates) version(image string) string {
	if u == nil {
		return ""
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.latest[image]
}

// behind tells whether a build of an image is older than its latest
// release.
func (u *updates) behind(image, running string) bool {
	return versionBehind(running, u.version(image))
}

const (
	mediaOCIIndex     = "application/vnd.oci.image.index.v1+json"
	mediaDockerList   = "application/vnd.docker.distribution.manifest.list.v2+json"
	mediaOCIManifest  = "application/vnd.oci.image.manifest.v1+json"
	mediaDockerManif  = "application/vnd.docker.distribution.manifest.v2+json"
	acceptedManifests = mediaOCIIndex + ", " + mediaDockerList + ", " + mediaOCIManifest + ", " + mediaDockerManif
)

// manifest is an image index or an image manifest, as much as is read here.
type manifest struct {
	MediaType string `json:"mediaType"`
	Manifests []struct {
		Digest   string `json:"digest"`
		Platform struct {
			OS           string `json:"os"`
			Architecture string `json:"architecture"`
		} `json:"platform"`
	} `json:"manifests"`
	Config struct {
		Digest string `json:"digest"`
	} `json:"config"`
}

// latestVersion reads the version label of an image's latest tag: a pull
// token, the tag's manifest (an index: one of its images, all built from
// the same commit), then that image's configuration.
func (u *updates) latestVersion(ctx context.Context, image string) (string, error) {
	repo := registryOwner + "/" + image
	var tok struct {
		Token string `json:"token"`
	}
	if err := u.get(ctx, "/token?scope="+url.QueryEscape("repository:"+repo+":pull"), "", "", &tok); err != nil {
		return "", err
	}
	var m manifest
	if err := u.get(ctx, "/v2/"+repo+"/manifests/latest", tok.Token, acceptedManifests, &m); err != nil {
		return "", err
	}
	if m.MediaType == mediaOCIIndex || m.MediaType == mediaDockerList || len(m.Manifests) > 0 {
		digest := ""
		for _, d := range m.Manifests {
			// Attestations are in the index too, for the "unknown" platform.
			if d.Platform.OS != "linux" {
				continue
			}
			if digest == "" || d.Platform.Architecture == runtime.GOARCH {
				digest = d.Digest
			}
		}
		if digest == "" {
			return "", errors.New("latest has no linux image")
		}
		m = manifest{}
		if err := u.get(ctx, "/v2/"+repo+"/manifests/"+digest, tok.Token, mediaOCIManifest+", "+mediaDockerManif, &m); err != nil {
			return "", err
		}
	}
	if m.Config.Digest == "" {
		return "", errors.New("latest has no configuration")
	}
	var cfg struct {
		Config struct {
			Labels map[string]string `json:"Labels"`
		} `json:"config"`
	}
	if err := u.get(ctx, "/v2/"+repo+"/blobs/"+m.Config.Digest, tok.Token, "", &cfg); err != nil {
		return "", err
	}
	v := cfg.Config.Labels[versionLabel]
	if v == "" {
		return "", errors.New("latest has no " + versionLabel + " label")
	}
	return v, nil
}

// get reads a JSON document of the registry. A blob may be redirected to
// another host, which does not get the token.
func (u *updates) get(ctx context.Context, path, token, accept string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.base+path, nil)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := u.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", strings.SplitN(path, "?", 2)[0], resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, maxRegistryDoc)).Decode(v)
}

// How this eriphe-core was installed (install.sh and install.ps1 say it in
// ERIPHE_INSTALL): it gives the command that updates it.
var coreInstall = os.Getenv("ERIPHE_INSTALL")

// updateCommand is a way to update a machine: a command and what it is for.
type updateCommand struct {
	Label string // a text key
	Cmd   string
}

// updateCommands are the commands that update a machine installed one way:
// the installer run again (it keeps its options), the Eriphe container's
// launcher (docker: install/eriphe-launcher), or the compose project pulled
// again. Installed by hand, the machine gets all of them.
func updateCommands(install string) []updateCommand {
	unix := updateCommand{"update.unix", "curl -fsSL https://github.com/Alexis-Marcel/eriphe-system/releases/latest/download/install.sh | sh -s"}
	windows := updateCommand{"update.windows", "irm https://github.com/Alexis-Marcel/eriphe-system/releases/latest/download/install.ps1 | iex"}
	compose := updateCommand{"update.compose", "docker compose pull && docker compose up -d"}
	switch install {
	case "linux", "macos":
		return []updateCommand{unix}
	case "windows":
		return []updateCommand{windows}
	case "docker":
		return []updateCommand{{"update.docker", "docker exec eriphe eriphe-launcher update"}}
	}
	return []updateCommand{compose, unix, windows}
}

// versionStatus is a build's version next to the latest one, for the API.
type versionStatus struct {
	Version  string   `json:"version"`
	Latest   string   `json:"latest,omitempty"` // empty while unknown
	Behind   bool     `json:"update_available"` // a newer build is published
	Commands []string `json:"update_commands,omitempty"`
}

func (c *core) versionStatus(image, running, install string) versionStatus {
	s := versionStatus{Version: running, Latest: c.updates.version(image), Behind: c.updates.behind(image, running)}
	if s.Behind {
		for _, u := range updateCommands(install) {
			s.Commands = append(s.Commands, u.Cmd)
		}
	}
	return s
}

// handleVersion is eriphe-core's version and the latest one, for the CLI:
// GET /v1/version.
func (c *core) handleVersion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"core": c.versionStatus("eriphe-core", version, coreInstall),
		"node": map[string]string{"latest": c.updates.version("eriphe-node")},
	})
}
