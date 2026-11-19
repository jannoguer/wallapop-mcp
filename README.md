# wallapop-mcp

MCP server for Wallapop's unofficial web API: search listings and check sellers anonymously, or manage your own account with a session cookie. Single Go binary, stdio.

```sh
go build -o wallapop-mcp.exe ./cmd/wallapop-mcp
claude mcp add wallapop -e WALLAPOP_CITY=Barcelona -- C:\path\to\wallapop-mcp.exe
```

Tools: `search_items`, `search_enriched`, `get_item`, `get_seller`, `list_categories`. With `WALLAPOP_SESSION` set, also: `my_items`, `inbox`, `get_conversation`, `send_message`, `edit_item`, `set_item_status`, `create_listing`.

| Env var | Default | Meaning |
|---|---|---|
| `WALLAPOP_CITY` | none | City used when a search omits `city` (Spain, Italy, Portugal). Without either, Wallapop uses the server's IP location |
| `WALLAPOP_MIN_INTERVAL_MS` | `250` | Minimum gap between Wallapop requests |
| `WALLAPOP_SESSION` | none | Session cookie; enables the account tools |

The server reads these from its process environment, not from a `.env` file. Your MCP client passes them from the `env` block of its JSON config: `claude mcp add --scope user -e NAME=value ...` writes it to `~/.claude.json`, and Claude Desktop reads it from `claude_desktop_config.json`. To commit a project `.mcp.json` instead, write `"WALLAPOP_SESSION": "${WALLAPOP_SESSION}"` there and Claude Code expands it from your shell, so the cookie stays out of the repo.

## Session cookie

Log in at es.wallapop.com. Open DevTools > Application > Cookies > `https://es.wallapop.com` and copy the value of `__Secure-next-auth.session-token`. Wallapop rotates the cookie, and the server saves each new one to `%APPDATA%\wallapop-mcp\session.json` (the user config dir on other systems). Each new cookie is valid for 30 days, so you only need to paste it again if the server goes a month without running. Changing `WALLAPOP_SESSION` replaces the saved cookie. The cookie gives full access to your account, so keep it out of repos.

## Test

```sh
go test ./...                                        # offline fixtures
go test -tags live -run Live ./...                   # real API; run first when results look off
go test -tags "live livewrite" -run LiveWrite ./...  # publishes, sells and deletes a listing: burner account only
```

## Caveats

- Wallapop's terms forbid automated access. Account tools act as you and can get the account banned, so try them on a spare account first.
- Not supported: offers, favourites, saved searches, purchases, pausing listings (Wallapop answers 403).
- `get_item` and `get_seller` make one extra request, to the es.wallapop.com page, for shipping and shipment data; `search_enriched` leaves that data out.
