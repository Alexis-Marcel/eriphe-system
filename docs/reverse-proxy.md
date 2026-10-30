# Eriphe behind your reverse proxy

If you already run a reverse proxy (Traefik, Nginx Proxy Manager, Caddy…) with your own domain and
certificate, Eriphe can sit behind it at a name of yours, e.g. `https://eriphe.example.org`. With no
domain and no proxy, use **Remote access** instead (Settings → Remote access): Eriphe joins your
Tailscale network by itself, with a certificate browsers trust.

Keep it private: route the name on your local network and over your VPN (split DNS, an IP allow
list on the proxy), not on the internet. Exposing the workspace publicly is not supported.

What goes through the proxy is the **workspace** only (the browser and the phone app). Your
**devices** keep talking to eriphe-core on its API port, `8800`, with mutual TLS: do not route that
port through the proxy (see [Devices away from home](#devices-away-from-home)).

## 1. Route the name to the workspace

The workspace listens on port **8443**, in **HTTPS only**, with a self-signed certificate: the proxy
connects to it in HTTPS and skips the verification of that certificate (or pins it). There is no
plain HTTP listener. Its address, from the proxy:

- the same Docker network as eriphe-core: `https://eriphe-core:8443`;
- otherwise: the host's address and published port, e.g. `https://192.0.2.10:8443`.

The workspace keeps a live connection open (Server-Sent Events on `/events`, with
`X-Accel-Buffering: no`) and uses no WebSocket. A proxy passes it as is, as long as it does not
buffer the answer or close it after a short timeout.

The proxy must send `X-Forwarded-For` and `X-Forwarded-Proto`, and keep the request's `Host` or
send `X-Forwarded-Host`: the three below do by default.

### Traefik

With Docker labels on eriphe-core's container (the network is the one Traefik shares with it):

```yaml
labels:
  - traefik.enable=true
  - traefik.http.routers.eriphe.rule=Host(`eriphe.example.org`)
  - traefik.http.routers.eriphe.entrypoints=websecure
  - traefik.http.routers.eriphe.tls=true
  - traefik.http.services.eriphe.loadbalancer.server.port=8443
  - traefik.http.services.eriphe.loadbalancer.server.scheme=https
  - traefik.http.services.eriphe.loadbalancer.serverstransport=eriphe@file
```

And, in a file of the dynamic configuration, the transport that accepts the self-signed certificate:

```yaml
http:
  serversTransports:
    eriphe:
      insecureSkipVerify: true
```

An IP allow list middleware (`ipAllowList`) on the router keeps it to your local network and VPN.
Server-Sent Events go through Traefik unbuffered.

### Nginx Proxy Manager

Hosts → Proxy Hosts → Add Proxy Host:

- **Domain Names**: `eriphe.example.org`;
- **Scheme**: `https`, **Forward Hostname / IP**: `eriphe-core` (or the host's address),
  **Forward Port**: `8443`. NPM does not check the certificate of an HTTPS backend;
- **Websockets Support**: on (harmless; Eriphe does not need it);
- **SSL**: your certificate, **Force SSL** on;
- **Advanced**, so the live updates are not buffered or cut:

  ```nginx
  proxy_buffering off;
  proxy_read_timeout 1h;
  ```

An **Access List** (your local network and VPN ranges) keeps it private.

### Caddy

```caddyfile
eriphe.example.org {
	reverse_proxy https://eriphe-core:8443 {
		transport http {
			tls_insecure_skip_verify
		}
		flush_interval -1
	}
}
```

Caddy passes the original `Host` and the `X-Forwarded-*` headers. `flush_interval -1` sends the live
updates at once (Caddy does it for `text/event-stream` anyway).

## 2. Tell Eriphe its address and its proxy

Open the workspace (at its local address, the first time), then **Settings → Account (Mon espace) →
Workspace address**:

- **Workspace address**: `https://eriphe.example.org`, without a path. The sign-in with a provider
  comes back to it, the tools' and Google Drive's OAuth redirect URI is
  `https://eriphe.example.org/oauth/callback`, and forms sent from it are the workspace's own. A
  sign-in or a tool's authorization must start from that address: from another one, Eriphe says
  where to open it.
- **Trusted proxies**: the address the proxy connects from, as eriphe-core sees it: one per line,
  addresses or CIDR ranges, e.g. the Docker network `172.18.0.0/16` or the host's `192.0.2.10`. The
  panel shows the address this page comes from, which helps to find it: open the panel through the
  proxy. From these addresses only, Eriphe believes `X-Forwarded-For` (for the browser's address,
  e.g. the limit on password attempts, which would otherwise count every visitor as the proxy),
  `X-Forwarded-Proto` and `X-Forwarded-Host`. From anywhere else they are ignored. The same list is
  the one the proxy sign-in (below) believes its user header from.

Then open `https://eriphe.example.org` and sign in there: the session of the local address does not
follow you to the new name.

The same in the configuration file, which then wins (the panel shows them locked):

```json
"web": {
  "external_url": "https://eriphe.example.org",
  "auth": {"proxy": {"trusted": ["172.18.0.0/16"]}}
}
```

`web.auth.external_url` works too (it is the same value); with `web.auth` in the file, the sign-in
settings are the file's as a whole.

## 3. Sign in with your identity provider (optional)

With the address set, Settings → Account → Sign-in → **Identity provider** (OpenID Connect:
Authentik, Authelia, Keycloak, PocketID…):

1. At the provider, create a confidential OAuth2/OpenID client with the redirect URI
   `https://eriphe.example.org/auth/callback` (the form shows it), the scopes `openid`, `email` and
   `profile`.
2. In Eriphe, give the issuer, the client ID and secret and who may enter (the provider's account
   ids, or verified emails), **Test**, then **Try the sign-in** and **Save**.

Do not also put the provider's forward-auth in front of Eriphe at the proxy: Eriphe signs you in
itself. The password still opens the workspace from your local network, without the proxy, unless
you turn that off. `docker exec eriphe-core eriphe-core auth reset` goes back to the password.

If your proxy signs users in itself and passes the user in a header (Authelia, Authentik's proxy
outpost), choose **Reverse proxy** instead: the header is believed from the trusted proxies only.

## Devices away from home

Devices reach eriphe-core on port 8800, never through the proxy: at home, the local address. To
keep a laptop working away from home, give them a second address, tried when the first one does not
answer:

- **Remote access** on: the "Connect a device" commands add its address on the tailnet by
  themselves;
- otherwise, Tailscale (or another VPN) on the host: set its address in **Second address for the
  devices** (same panel), e.g. `http://100.64.0.2:8800`. The commands then give
  `--core http://192.0.2.10:8800,http://100.64.0.2:8800`.

Every installer takes the list (`install.sh --core`, `install.ps1 -Core` and `ERIPHE_CORE`, the
container's and compose's `ERIPHE_CORE`, `eriphe-node -core`). A device already connected takes the
new list when its install command runs again with `--core` and no token (`-Core` on Windows,
`ERIPHE_CORE` for the container); it keeps its identity.
