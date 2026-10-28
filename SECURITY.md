# Security policy

Eriphe holds credentials, runs agents on your machines and acts on your behalf, so security
reports are welcome and taken seriously.

## Reporting a vulnerability

Please do not open a public issue. Report it privately through GitHub's private vulnerability
reporting: the repository's **Security** tab → **Report a vulnerability**.

Include what you found, how to reproduce it, which component it affects (eriphe-core, eriphe-node,
the worker image, an installer, an MCP server, the Home Assistant integration) and the version
(`sha-<commit>`, shown in Settings → Machines). You should get an answer as soon as possible, usually within a few days. Once a fix
is published, the advisory credits you unless you prefer otherwise.

## Supported versions

Eriphe is alpha: only the latest images (`ghcr.io/alexis-marcel/eriphe-*:latest`) and the `main`
branch get fixes.

## Scope

In scope: anything that lets a task, a tool's content (mail, web page, MCP result), a machine on
the LAN or another user reach what it should not: credentials, other tasks, the host, the LAN, or
actions the policy should have stopped. The design and its known limits are described in
core/README.md (Security model, Permission policy), node/README.md and deploy/README.md (What the
proxy does not stop).

Out of scope: the terms or behaviour of the AI providers and agents themselves (Claude Code,
Codex, OpenCode), and attacks that need the owner's own credentials or root on a machine running
Eriphe.
