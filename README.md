<h1 align="center">mcpgen</h1>

<p align="center">
  <b>Any OpenAPI spec → an MCP server your AI can call.</b><br/>
  One YAML. One binary. Three transports. Pluggable auth.
</p>

<p align="center">
  <a href="https://go.dev/"><img src="https://img.shields.io/badge/Go-1.26%2B-00ADD8?logo=go&logoColor=white" alt="Go 1.26+"></a>
  <a href="https://gofr.dev/"><img src="https://img.shields.io/badge/built%20with-GoFr-7B61FF" alt="Built with GoFr"></a>
  <a href="https://modelcontextprotocol.io/"><img src="https://img.shields.io/badge/MCP-compatible-0A84FF" alt="MCP compatible"></a>
  <a href="./LICENSE"><img src="https://img.shields.io/badge/license-MIT-green.svg" alt="MIT license"></a>
</p>

---

```yaml
# config.yaml — that's it
spec: { url: https://api.example.com/openapi.json }
auth: { provider: bearer_static, bearer_static: { token_env: API_TOKEN } }
filter: { include_tags: [Users, Orders] }
```

```sh
$ mcpgen serve --config=config.yaml
mcpgen loaded 42 tools, transport=stdio
```

Your AI can now call every `Users` and `Orders` endpoint. No code generated, no plumbing written. Point it at a different spec tomorrow and you have a different MCP.

---

## Why this exists

OpenAPI specs are everywhere. MCP-compatible AI is everywhere. Bridging the two takes about 30 lines of YAML now.

What the existing tools get wrong:

- **They give up on auth.** Most APIs (eazyupdates, GitHub, Notion, your own) use refresh-token flows. Every other OpenAPI→MCP project hands you a static-bearer config and walks away.
- **Object bodies look ugly.** Generators expose `postUpdate(body={remarks: "…", date: "…"})`. We expose `postUpdate(remarks="…", date="…")` — flatter, what LLMs actually want.
- **`$ref` confuses LLMs.** They get unresolved JSON pointers. We inline.

## Install

Pick the path that matches you. They all give you the same `mcpgen` binary.

### Just want to use it (Go installed)

```sh
go install github.com/aryanmehrotra/mcpgen/cmd/mcpgen@latest
```

Drops the binary in `$(go env GOBIN)` (or `$HOME/go/bin`). Make sure that's in your `$PATH`.

### No Go? Grab a prebuilt binary

Download the archive for your OS/arch from the [latest release](https://github.com/aryanmehrotra/mcpgen/releases/latest), extract `mcpgen`, and drop it in `$PATH`.

```sh
# macOS arm64 example
curl -sL https://github.com/aryanmehrotra/mcpgen/releases/latest/download/mcpgen_v1.0.0_macos_arm64.tar.gz | tar xz
sudo mv mcpgen /usr/local/bin/
```

### Hacking on it

```sh
git clone https://github.com/aryanmehrotra/mcpgen
cd mcpgen
make install         # → ~/.local/bin/mcpgen
```

## Wire into Claude Code

```sh
claude mcp add my-api -e API_TOKEN=… -- mcpgen serve --config=$(pwd)/config.yaml
```

That's the whole integration. Ask Claude in the next session: *"List the first 5 users."* It'll call the tool.

## Auth providers

| Provider | When to use | Config |
|---|---|---|
| `bearer_static` | Service tokens, GitHub PATs, anything that doesn't expire | `token_env: MY_TOKEN` |
| `api_key` | Header / query / cookie keys | `in:`, `name:`, `value_env:` |
| `refresh_token` | OAuth/SSO-backed APIs with short-lived access tokens | see below |
| `none` | Public APIs | — |

### The `refresh_token` flow (the differentiator)

```yaml
auth:
  provider: refresh_token
  refresh_token:
    refresh_endpoint: https://api.example.com/auth/refresh
    refresh_in: header                          # header (default) | body
    refresh_token_field: refreshToken           # header name or JSON body field
    access_token_field: accessToken             # JSON field in the refresh response
    cache_path: ~/.config/my-api/cache.json     # chmod 0600 on write
    bootstrap_env: MY_REFRESH_TOKEN             # read once if cache empty
```

What you get:
- Bootstrap once from env, then the cache file is authoritative.
- Skew calculated from the JWT `exp` claim (or `expires_in` if your API gives one).
- A GoFr cron job pre-refreshes every 5 min when running daemon transports (`--transport=http|sse`), so the next tool call never blocks.
- Rotation: if the refresh response includes a fresh refresh token, the cache is updated.

## Reference example: eazyupdates

`examples/eazyupdates/` is a working config against [eazyupdates.com](https://eazyupdates.com) — a ~200-endpoint daily-standup API behind Google SSO. Paste a refresh token once, MCP keeps minting access tokens forever.

```sh
export EAZYUPDATES_REFRESH_TOKEN='paste-from-devtools'
claude mcp add eazyupdate \
  -e EAZYUPDATES_REFRESH_TOKEN \
  -- mcpgen serve --config=$(pwd)/examples/eazyupdates/config.yaml
```

What you can ask Claude:

> *"Did I post today's update? If not, look at my GitHub activity in the gofr-dev and zopdev orgs since midnight IST and post the summary."*

See [`examples/eazyupdates/README.md`](./examples/eazyupdates/README.md) for the 30-second setup.

<details>
<summary><b>Full config reference</b></summary>

```yaml
spec:
  url:  https://...           # OR
  file: ./openapi.yaml

server:
  name:      my-api-mcp       # advertised to MCP clients
  version:   0.1.0
  transport: stdio            # stdio | http | sse — overridable via --transport
  addr:      :8080            # http/sse only — overridable via --addr
  base_url:  https://api...   # falls back to the first `servers:` URL in the spec

auth:
  provider: bearer_static | api_key | refresh_token | none
  bearer_static:
    token:     literal-token
    token_env: TOKEN_ENV_VAR
  api_key:
    in:        header | query | cookie
    name:      X-API-Key
    value:     literal-value
    value_env: VALUE_ENV_VAR
  refresh_token:
    refresh_endpoint:    https://api.example.com/auth/refresh
    refresh_in:          header | body
    refresh_token_field: refreshToken
    access_token_field:  accessToken
    expires_in_field:    expiresIn               # optional
    cache_path:          ~/.config/my-api/cache.json
    bootstrap_env:       MY_REFRESH_TOKEN
    skew_seconds:        60

filter:
  include_tags: []            # OR-match on operation tags; empty = allow all
  exclude_tags: []            # wins over include_tags
  include_ops:  []            # operationId allowlist
  exclude_ops:  []            # operationId blocklist
```

CLI:

```sh
mcpgen validate --config=config.yaml      # parse + report tool count
mcpgen tools    --config=config.yaml      # dump generated tools as JSON
mcpgen serve    --config=config.yaml      # stdio (default)
mcpgen serve    --config=config.yaml --transport=http --addr=:8080
mcpgen serve    --config=config.yaml --transport=sse  --addr=:8081
```

</details>

<details>
<summary><b>Architecture</b></summary>

```
cmd/mcpgen/     CLI: gofr.NewCMD + serve|validate|tools
                  Registers GoFr HTTPService for the upstream;
                  installs cron for proactive token refresh.

pkg/spec/         Loads OpenAPI v3 (kin-openapi) from URL or file.

pkg/tools/        Spec → []ToolDef. Tag/op filtering,
                  object-body flattening, $ref inlining w/ cycle break.

pkg/auth/         Provider interface: Apply(ctx, http.Header, url.Values).
                  bearer_static | api_key | refresh_token.
                  Optional Refresher interface for cron-driven refresh.

pkg/upstream/     HTTPTransport interface: Do(ctx, Request).
                  stdlib net/http or GoFr-instrumented service.HTTP.
                  Client.Call splits into pure helpers (bucketArgs,
                  encodeBody, expandPath, applyAuth, transport.Do).

pkg/mcpsrv/       Ties spec + tools + auth + transport into one App.
                  Wraps mark3labs/mcp-go; serves stdio / HTTP / SSE.
```

</details>

<details>
<summary><b>Prior art</b></summary>

| Project | Approach | Auth | Tool surface | Lang |
|---|---|---|---|---|
| [ckanthony/openapi-mcp](https://github.com/ckanthony/openapi-mcp) | Runtime | API key | All ops | Go |
| [AWS Labs openapi-mcp-server](https://awslabs.github.io/mcp/servers/openapi-mcp-server) | Runtime | Basic/Bearer/API key/Cognito | Tag include-exclude | Python |
| [salacoste/openapi-mcp-swagger](https://github.com/salacoste/openapi-mcp-swagger) | Meta-tools | — | Always 2-hop | Python |
| [LostInBrittany/swagger-to-mcp-generator](https://github.com/LostInBrittany/swagger-to-mcp-generator) | Codegen | — | All ops | Java |
| [higress-group/openapi-to-mcpserver](https://github.com/higress-group/openapi-to-mcpserver) | Codegen | Via gateway | — | Go |

</details>

## Build / test

```sh
make build   # → bin/mcpgen
make test    # go test ./...
```

## Contributing

Issues, PRs, design notes — all welcome. Especially:
- More auth providers (OAuth 2.0 PKCE, OAuth client-credentials, mTLS)
- Hot-reload of spec on file change
- Structured error pass-through (so LLMs see typed upstream errors)

## License

[MIT](./LICENSE) — built on [GoFr](https://gofr.dev) + [mark3labs/mcp-go](https://github.com/mark3labs/mcp-go).
