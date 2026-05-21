# Example: eazyupdates

A working `mcpgen` configuration for [eazyupdates.com](https://eazyupdates.com), the daily-standup product. ~200 endpoints in the spec; the default filter exposes the Update / Employee / Project Controllers (~105 tools) — narrow further if your MCP client gets noisy.

## One-time setup (~30 seconds)

1. Sign in to `https://eazyupdates.com` in your browser (Google SSO, as normal).
2. Open DevTools → **Application** → **Local Storage** → `https://eazyupdates.com`.
3. Copy the `refreshToken` value.
4. Export it:
   ```sh
   export EAZYUPDATES_REFRESH_TOKEN='paste-here'
   ```

The bridge writes a cache at `~/.config/eazyupdate-mcp/cache.json` (`chmod 0600`). After the first call the env var is optional — the cache is authoritative.

## Wire into Claude Code

```sh
claude mcp add eazyupdate \
  -e EAZYUPDATES_REFRESH_TOKEN="$EAZYUPDATES_REFRESH_TOKEN" \
  -- $(which mcpgen) serve --config=$(pwd)/config.yaml
```

## Try it from the CLI first

```sh
mcpgen validate --config=config.yaml
# OK: 105 tools generated (base=https://api.eazyupdates.com)

mcpgen tools --config=config.yaml | jq '.[].name' | head
```

## What you can ask Claude once it's wired

- *"Did I post today's update?"* → `getEmployeeUpdatesV4` for today's date.
- *"Show me my last two weeks of updates on zop.dev."* → `getEmployeeUpdatesV4` with a date range, filtered to one project.
- *"Who on my team didn't post yesterday?"* → `getSubOrdinatesNoUpdatesV2`.
- *"Post today's update for gofr.dev: …"* → `postUpdate` with flattened args.
- *"Fix yesterday's update — change 'open' to 'merged' on PR #1374."* → `editUpdates`.

## Narrowing further

The default filter keeps three controllers. To go tighter (e.g. just posting and reading your own updates):

```yaml
filter:
  include_tags: [Update Controller]
```

That drops the tool count from 105 → 7.
