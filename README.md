# wallapop-mcp

MCP server over Wallapop's internal web API. Anonymously, an agent can search second-hand listings and judge them by item details and seller reputation. With your session cookie, it can also manage your own account: listings, chats and new listings.

Single Go binary, stdio transport, no API key.

## Install

```sh
go build -o wallapop-mcp.exe .
claude mcp add wallapop -e WALLAPOP_CITY=Barcelona -- C:\path\to\wallapop-mcp.exe
```

Claude Desktop (`claude_desktop_config.json`):

```json
{
  "mcpServers": {
    "wallapop": {
      "command": "C:\\path\\to\\wallapop-mcp.exe",
      "env": { "WALLAPOP_CITY": "Barcelona" }
    }
  }
}
```

## Tools

| Tool | Does |
|---|---|
| `search_items` | Search listings, 40 per page, with a cursor for the next page |
| `search_enriched` | Search, then attach item details and seller reputation to the top N (default 10, max 20) |
| `get_item` | Full listing: description, condition, attributes, views/favorites/conversations, images |
| `get_seller` | Rating, reviews, sales, purchases, reports received, account age, recent reviews |
| `list_categories` | Find category ids by name (Spanish names, accents optional) |

With `WALLAPOP_SESSION` set, these are added:

| Tool | Does |
|---|---|
| `my_items` | My active listings (reserved included) with views, favorites and chats; or my sold ones |
| `inbox` | My chats, most recent first, with the last message of each |
| `get_conversation` | Messages of one chat, oldest first |
| `send_message` | Send a chat message, by conversation or by listing; a retry with the same text is not sent twice |
| `edit_item` | Change only the given fields of my listing; can append photos |
| `set_item_status` | Reserve, unreserve, mark sold (irreversible) or delete (permanent) |
| `create_listing` | Publish a listing with 1-10 local jpeg/png photos; a missing required attribute is reported with its allowed values |

Search filters: `query`, `city`, `radius_km`, `category_id` (any depth), `min_price`, `max_price`, `conditions` (`un_opened`, `new`, `as_good_as_new`, `good`, `fair`, `has_given_it_all`), `posted_within` (`today`, `week`, `month`), `sort` (`relevance`, `closest`, `newest`, `price_asc`, `price_desc`), `shippable_only`, `cursor`.

Tip for agents: pass a `category_id` (e.g. Smartphones, 9447). Keyword search alone mixes in cases and cables. Keyword matching is fuzzy ("iphone 13" also returns iPhone 11s), so check `attributes.model` in enriched results.

## Config

| Env var | Default | Meaning |
|---|---|---|
| `WALLAPOP_CITY` | none | City used when a call omits `city`. Without either, Wallapop uses the server's IP location |
| `WALLAPOP_MIN_INTERVAL_MS` | `250` | Minimum gap between Wallapop requests |
| `WALLAPOP_SESSION` | none | Your session cookie; enables the account tools |

### Session cookie

1. Log in at es.wallapop.com.
2. DevTools > Application > Cookies > `https://es.wallapop.com`: copy the value of `__Secure-next-auth.session-token`.
3. Pass it as `WALLAPOP_SESSION` (e.g. `claude mcp add wallapop -e WALLAPOP_SESSION=... -- C:\path\to\wallapop-mcp.exe`).

The server exchanges the cookie for 5-minute API tokens. Wallapop returns a refreshed cookie, valid for 30 days, each time, and the server saves it to `<user config dir>/wallapop-mcp/session.json` (on Windows `%APPDATA%`). So the session keeps working as long as the server runs at least once a month. When it does expire, tools report it: paste a fresh cookie. Changing `WALLAPOP_SESSION` makes the server use the new value.

Cities are geocoded with OpenStreetMap Nominatim (Spain, Italy, Portugal), at most 1 request/s, cached per process.

## Test

```sh
go test ./...                       # offline, against recorded fixtures
go test -tags live -run Live ./...  # against the real API, catches drift; account reads too if WALLAPOP_SESSION is set
go test -tags "live livewrite" -run LiveWrite ./...  # publishes, edits, sells and deletes a listing: burner accounts only
```

## Caveats

- Unofficial API: endpoints, params and the `X-DeviceOS` header requirement can change without notice. The live test is the first thing to run when results look off.
- Wallapop's terms forbid automated access. This is meant for low-volume personal use. The account tools act as you, so automation can get the account flagged or banned; try them on a spare account first.
- The session cookie gives full access to your account: keep it out of shared config and repos.
- A listing published seconds ago refuses edits and status changes for a few seconds; the tools say so, retry.
- Not supported: offers, favourites, saved searches, purchases, pausing listings (Wallapop answers 403).
- `next_cursor` from `search_enriched` continues at the next 40-result page; listings on the current page past `top_n` are skipped.
