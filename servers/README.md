# Eriphe's tool servers

`mail`, `calendar` and `files` are MCP servers that eriphe-core launches on a machine (a node)
and configures. They follow one contract, so another image can take the place of one of them.

## Configuration

- The server reads its configuration as JSON from `TOOL_CONFIG`, or from the file named by
  `TOOL_CONFIG_FILE` (or `-config`). eriphe-core writes it.
- `accounts` lists the server's accounts. Each one has a `name` (how tools name it in their
  `account` argument) and the fields of its kind of tool.
- An account's `auth` names where its secret is: exactly one of `password_env`, `password_file`,
  `token_env` or `token_file` (the ones the server accepts).
  - A token is the JSON object `{"client_id", "client_secret", "refresh_token", "token_url"}`
    lent by eriphe-core from its vault. The server refreshes it itself, at `token_url`.
  - eriphe-core names the variables `TOOL_PASSWORD_<h>` and `TOOL_TOKEN_<h>`, where `<h>` is a
    short hash of the account's name.
- `read_only: true` on an account: the server refuses every write through it.
- `scopes` on an account (when the server needs them): what the account's connection granted.
- `places` (a server that has them): what the user gave to Eriphe inside the accounts. Each place
  is `{name, account, id, access}`; the server reaches only these.

The keys a server knows beyond these are its own (`timezone`, limits…); eriphe-core keeps them as
they are when it rewrites the configuration.

## Setup tools

A tool marked `_meta: {"eriphe/setup": true}` is kept for eriphe-core's workspace: the pool leaves
it out of the server's tools, so no task and not the brain ever get it. `files` serves
`browse_folders`, which lists an account's folders for the places page.

Each server's README gives its tools and its account fields: [mail](mail/README.md),
[calendar](calendar/README.md), [files](files/README.md).
