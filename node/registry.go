package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// A pull client for public images, enough for the agents' (inject.go): an
// anonymous token if the registry asks for one, the manifest of this
// platform, its layers. Everything is checked against its digest. Docker's
// and Apple's container's own pulls are not used: the node does not get an
// image's files from them through the NAS's socket proxy, nor from
// container.

const (
	mediaOCIIndex    = "application/vnd.oci.image.index.v1+json"
	mediaDockerList  = "application/vnd.docker.distribution.manifest.list.v2+json"
	mediaOCIManifest = "application/vnd.oci.image.manifest.v1+json"
	mediaDockerImage = "application/vnd.docker.distribution.manifest.v2+json"
	maxRegistryDoc   = 4 << 20
)

// imageRef is an image reference split for the registry API.
type imageRef struct {
	Registry string // host[:port], as given; docker.io for Docker Hub
	Repo     string
	Ref      string // tag or sha256:digest
}

// parseImageRef reads name[:tag][@sha256:digest], as docker does: a first
// part with a dot, a colon or localhost is the registry, Docker Hub
// otherwise.
func parseImageRef(s string) (imageRef, error) {
	var r imageRef
	if s == "" || strings.ContainsAny(s, " \t\n") || strings.HasPrefix(s, "-") {
		return r, fmt.Errorf("bad image %q", s)
	}
	name := s
	if i := strings.Index(s, "@"); i >= 0 {
		name, r.Ref = s[:i], s[i+1:]
		if !strings.HasPrefix(r.Ref, "sha256:") || len(r.Ref) != 71 {
			return r, fmt.Errorf("bad digest in %q", s)
		}
	}
	if i := strings.LastIndex(name, ":"); i >= 0 && !strings.Contains(name[i:], "/") {
		if r.Ref == "" {
			r.Ref = name[i+1:]
		}
		name = name[:i]
	}
	if r.Ref == "" {
		r.Ref = "latest"
	}
	first, rest, ok := strings.Cut(name, "/")
	if ok && (strings.ContainsAny(first, ".:") || first == "localhost") {
		r.Registry, r.Repo = first, rest
	} else {
		r.Registry, r.Repo = "docker.io", name
		if !strings.Contains(name, "/") {
			r.Repo = "library/" + name
		}
	}
	if r.Repo == "" || strings.Contains(r.Repo, "//") {
		return r, fmt.Errorf("bad image %q", s)
	}
	return r, nil
}

// base is the registry's API root: plain HTTP for a registry on the
// loopback only (a local test registry).
func (r imageRef) base() string {
	host := r.Registry
	if host == "docker.io" {
		host = "registry-1.docker.io"
	}
	h := host
	if i := strings.LastIndex(h, ":"); i >= 0 {
		h = h[:i]
	}
	if h == "localhost" || h == "127.0.0.1" || plainRegistries[host] {
		return "http://" + host
	}
	return "https://" + host
}

// plainRegistries are other registries reached over plain HTTP: tests only.
var plainRegistries = map[string]bool{}

type registryClient struct {
	http *http.Client
	mu   sync.Mutex
	tok  map[string]string // repository → token
}

func newRegistryClient() *registryClient {
	return &registryClient{http: &http.Client{Timeout: 30 * time.Minute}, tok: map[string]string{}}
}

// imageManifest is an image index or manifest, as much as is read here.
type imageManifest struct {
	MediaType string `json:"mediaType"`
	Manifests []struct {
		Digest   string `json:"digest"`
		Platform struct {
			OS           string `json:"os"`
			Architecture string `json:"architecture"`
		} `json:"platform"`
	} `json:"manifests"`
	Layers []struct {
		MediaType string `json:"mediaType"`
		Digest    string `json:"digest"`
		Size      int64  `json:"size"`
	} `json:"layers"`
}

// platformManifest is the image's manifest for os/arch and its digest:
// through the index when the tag names one.
func (c *registryClient) platformManifest(ctx context.Context, ref imageRef, os, arch string) (imageManifest, string, error) {
	accept := strings.Join([]string{mediaOCIIndex, mediaDockerList, mediaOCIManifest, mediaDockerImage}, ", ")
	m, digest, err := c.manifest(ctx, ref, ref.Ref, accept)
	if err != nil {
		return m, "", err
	}
	if len(m.Manifests) > 0 || m.MediaType == mediaOCIIndex || m.MediaType == mediaDockerList {
		pick := ""
		for _, d := range m.Manifests {
			if d.Platform.OS == os && d.Platform.Architecture == arch {
				pick = d.Digest
				break
			}
		}
		if pick == "" {
			return m, "", fmt.Errorf("%s has no %s/%s image", ref.Ref, os, arch)
		}
		if m, digest, err = c.manifest(ctx, ref, pick, mediaOCIManifest+", "+mediaDockerImage); err != nil {
			return m, "", err
		}
	}
	if len(m.Layers) == 0 {
		return m, "", errors.New("the image has no layer")
	}
	return m, digest, nil
}

// manifest reads one manifest and its digest, checked when it was asked by
// digest.
func (c *registryClient) manifest(ctx context.Context, ref imageRef, which, accept string) (imageManifest, string, error) {
	var m imageManifest
	resp, err := c.get(ctx, ref, "/manifests/"+which, accept)
	if err != nil {
		return m, "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxRegistryDoc+1))
	if err != nil {
		return m, "", err
	}
	if len(raw) > maxRegistryDoc {
		return m, "", errors.New("manifest too large")
	}
	sum := sha256.Sum256(raw)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	if strings.HasPrefix(which, "sha256:") && digest != which {
		return m, "", fmt.Errorf("manifest %s: its content is %s", which, digest)
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return m, "", fmt.Errorf("manifest: %w", err)
	}
	if m.MediaType == "" {
		m.MediaType = strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0])
	}
	return m, digest, nil
}

// blob opens a layer; the caller checks its digest as it reads it.
func (c *registryClient) blob(ctx context.Context, ref imageRef, digest string) (io.ReadCloser, error) {
	if !strings.HasPrefix(digest, "sha256:") || len(digest) != 71 {
		return nil, fmt.Errorf("bad layer digest %q", digest)
	}
	resp, err := c.get(ctx, ref, "/blobs/"+digest, "")
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// get calls the repository's API, with its token once the registry asked
// for one. A blob redirected to another host (a CDN) does not get it.
func (c *registryClient) get(ctx context.Context, ref imageRef, p, accept string) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, ref.base()+"/v2/"+ref.Repo+p, nil)
		if err != nil {
			return nil, err
		}
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		key := ref.Registry + "/" + ref.Repo
		c.mu.Lock()
		tok := c.tok[key]
		c.mu.Unlock()
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == http.StatusOK {
			return resp, nil
		}
		challenge := resp.Header.Get("WWW-Authenticate")
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			tok, err := c.token(ctx, challenge, ref.Repo)
			if err != nil {
				return nil, err
			}
			c.mu.Lock()
			c.tok[key] = tok
			c.mu.Unlock()
			continue
		}
		return nil, fmt.Errorf("GET %s%s: %s %s", ref.Registry, "/v2/"+ref.Repo+p, resp.Status, strings.TrimSpace(string(msg)))
	}
}

// token asks the registry's token service for an anonymous pull token, as
// its Bearer challenge says.
func (c *registryClient) token(ctx context.Context, challenge, repo string) (string, error) {
	scheme, params, _ := strings.Cut(challenge, " ")
	if !strings.EqualFold(scheme, "Bearer") {
		return "", fmt.Errorf("the registry asks for %q: only public images", scheme)
	}
	p := parseChallenge(params)
	realm, err := url.Parse(p["realm"])
	if err != nil || (realm.Scheme != "https" && realm.Scheme != "http") || realm.Host == "" {
		return "", fmt.Errorf("bad token realm %q", p["realm"])
	}
	q := realm.Query()
	if p["service"] != "" {
		q.Set("service", p["service"])
	}
	scope := p["scope"]
	if scope == "" {
		scope = "repository:" + repo + ":pull"
	}
	q.Set("scope", scope)
	realm.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, realm.String(), nil)
	if err != nil {
		return "", err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token: %s", resp.Status)
	}
	var t struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxRegistryDoc)).Decode(&t); err != nil {
		return "", err
	}
	if t.Token == "" {
		t.Token = t.AccessToken
	}
	if t.Token == "" {
		return "", errors.New("token: none given")
	}
	return t.Token, nil
}

// parseChallenge reads key="value" pairs, separated by commas.
func parseChallenge(s string) map[string]string {
	out := map[string]string{}
	for s != "" {
		s = strings.TrimLeft(s, " ,")
		k, rest, ok := strings.Cut(s, "=")
		if !ok {
			break
		}
		k = strings.TrimSpace(k)
		var v string
		if strings.HasPrefix(rest, `"`) {
			end := strings.Index(rest[1:], `"`)
			if end < 0 {
				break
			}
			v, s = rest[1:end+1], rest[end+2:]
		} else {
			v, s, _ = strings.Cut(rest, ",")
		}
		out[strings.ToLower(k)] = v
	}
	return out
}
