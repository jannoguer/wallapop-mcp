# Plan: authenticated account tools

Goal: let an agent manage my Wallapop account (listings, inbox, messages, new listings) with as few tool calls, schema bytes and response tokens as possible.

Status (2026-11-19): all phases implemented and verified live. Listings: `go test -tags "live livewrite"` on the burner. Messaging: burner to a main-account listing, with a reply from main, checked through the built server. "Verified" means we ran it on the burner. "Reported" means a third-party client documents it (see Sources) and we have not run it yet.

### Found during implementation
- Edits and status changes on a listing published less than ~5 s ago get 403 (edit) or 404 (reserve). The tools report "not ready yet, retry".
- Publishing needs no `location` (the listing goes to the account's location). Decimal prices work.
- `X-DeviceID` is not needed. An early 403 that looked like a missing header was the timing issue above.
- Real conversation fields: `hash`, `unread_messages`, `item.hash`, `item.title`, `with_user.name`, `with_user.hash`, `channel` (`chat.<other>.<conv>.<me>`); messages carry `from_self`, `text`, `type`, `status`, `timestamp` (epoch ms), newest first. The third-party names (`unread_counter`, `item.id`, `micro_name`) were wrong.
- Reading a thread does not mark it read. The inbox has no pagination header, so `inbox` takes `limit` only.

## Phase 0 results

### Auth (verified)
- The long-lived credential is the cookie `__Secure-next-auth.session-token` from `es.wallapop.com`. `GET https://es.wallapop.com/api/auth/session` with only that cookie returns `{"token": "<JWT>", ...}`.
- An invalid or expired cookie still gets HTTP 200, but with body `{}`. Treat a missing `token` as "session expired".
- The access JWT lives **5 minutes**. Claims include `hashed_sub` (my user hash, needed for chat) and `exp`.
- Every mint returns a fresh session cookie in `Set-Cookie`, with a sliding 30-day expiry. The previous cookie keeps working, so there is no reuse revocation.
- **Only `Authorization: Bearer` is required.** No `X-AppVersion`, `X-DeviceID`, `Origin` or browser User-Agent (Go's default UA works). The existing `X-DeviceOS: 0` stays because the anonymous CDN needs it.
- No request signing (none in the web bundles, and writes work without it).

### Endpoints (host `https://api.wallapop.com`)
| Need | Call | State |
|---|---|---|
| Me | `GET /api/v3/users/me` | verified; burner has no `location` field |
| My active listings | `GET /api/v3/items/mine/published?since=<cursor>` | verified; array of `{id, type, content:{title, sale_price, currency_code, flags:{reserved, sold, pending, banned, expired, onhold}, views, favorites, conversations, modified_date, web_slug, ...}}`; next cursor in `X-NextPage`. Reserved items are included with `flags.reserved` |
| My sold listings | `GET /sdui/selling/finished` | verified; widget tree, flatten `list_item_transaction` cards (title, date text, price) |
| Other `items/mine/*` statuses | `reserved`, `sold`, `inactive`, `active` | all 404 |
| Inbox | `GET /bff/messaging/inbox?page_size=N&max_messages=M` | verified (empty): `{user_hash, unread_messages, conversations[]}` |
| Unread count | `GET /api/v3/instant-messaging/messages/unread` | verified: `{unread_counter}` |
| IM token | `GET /api/v3/instant-messaging/token` | verified: 201 `{token, channels}` |
| Thread | `GET /bff/messaging/conversation/{id}` | verified |
| Open conversation | `POST /api/v3/instant-messaging/conversation` `{"item_hash_id":id}` | verified: `{conversation_id}` |
| Send message | PubNub REST `GET https://ps7.pndsn.com/publish/pub-c-255dc549-.../sub-c-89405e27-.../0/{channel}/0/{json}?meta=..&uuid=..&auth=<IM token>&pnsdk=PubNub-JS-Web/10.2.6` | verified: ack `[1,"Sent",tt]`, message delivered |
| Publish | `POST /api/v3/items` multipart `image` + `item` (JSON), `Accept: application/vnd.upload-v2+json` (exactly one Accept header, otherwise 405) | verified: 200 `{"id"}` |
| Extra photo | `POST /api/v3/items/{id}/picture2` multipart `image` + `order` | verified: 200, body is the **bare photo id as text**, not JSON |
| Edit | `PUT /api/v3/items/{id}`, upload-v2 Accept, full body incl. `pictures:[{id,order}]` | verified: 204 |
| Reserve / unreserve | `PUT /api/v3/items/{id}/reserve` `{"reserved":bool}` | verified: 204. Returned 404 for a few seconds right after publishing (eventual consistency) |
| Sold | `PUT /api/v3/items/{id}/sold` `{}` | verified: 204, `sold.flag` true, item leaves `items/mine/published` |
| Delete | `DELETE /api/v3/items/{id}` | verified: 204, then GET 404; works on sold items too |
| Pause / resume | `PUT /api/v3/items/{id}/inactivate` (and the bulk form) | **403 from CloudFront** with any headers; dropped from scope |
| Form schema | `POST /api/v3/items/upload/components` `{"fields":{summary, category_leaf_id, root_category_id},"mode":{"action":"upload","id":uuid}}` | verified: components with `id`, `type`, `is_required`, `data` |

Publish body (verified):
```json
{"attributes":{"title":"..","description":"..","condition":"as_good_as_new","price_amount":5},
 "category_leaf_id":"12485","apply_discount":false,
 "location":{"latitude":41.38,"longitude":2.16,"approximated":false},
 "delivery":{"allowed_by_user":false,"max_weight_kg":null,"cost_configuration_id":"814429d6-7844-471d-97af-196cd4020f26"},
 "upload_id":"<uuid>"}
```
- `cost_configuration_id` is a global constant from the web bundle, not per account: `BUYER_PAYS = 814429d6-7844-471d-97af-196cd4020f26`, `SELLER_PAYS = 04cf65ea-42f5-11ed-b878-0242ac120002`.
- The listing's location did not follow the coordinates sent. I sent Barcelona; the listing shows an approximated Banyoles. Phase 4 checks whether `location` matters at all.
- On edit, re-sending the item's own approximated location (`approximated: true`) was accepted and left the location unchanged.

## Design for low token cost

1. **Few tools, one sentence each.** Every tool schema is sent on every turn: 6 new tools, 11 total.
2. **Compact output.** `omitempty` everywhere. Dates as `2006-01-02`. No image URLs, raw objects or SDUI noise. Message text truncated to 160 chars in lists.
3. **Writes return a receipt** (`{"id":..,"status":"reserved"}`), never a re-fetch.
4. **Round trips:** inbox uses `max_messages=1`. `get_conversation` defaults to 20 messages. The JWT (5 min), IM token and user hash are cached per process.
5. **Byte budget test** on the marshaled `tools/list`. The go-sdk sends typed results as both `structuredContent` and JSON text (go-sdk `server.go:468`), so result structs are the real cost: keep them flat.

## Tools (registered only when a session is configured)

| Tool | Input | Output | Annotation |
|---|---|---|---|
| `my_items` | `status` (`active` default, `sold`), `cursor` | active: id, title, price, reserved, views, favorites, conversations, updated, `next_cursor`. sold: title, price, date | read-only |
| `inbox` | `unread_only`, `limit` (20; pagination unknown until a live capture) | conversation_id, item id+title, other user, last message (`me`/`them`, truncated), unread, date | read-only |
| `get_conversation` | `conversation_id`, `limit` (20) | other user, item, messages oldest-first `{from, text, date}` | read-only |
| `send_message` | `conversation_id` or `item_id`, `text` | `{conversation_id, status: sent, already_sent}` | write |
| `edit_item` | `id` + only the fields to change (title, description, price, condition, category_id, attributes, shippable, max_weight_kg, add_photos) | receipt | write, idempotent |
| `set_item_status` | `id`, `action`: reserve, unreserve, sold, delete | receipt | destructive |
| `create_listing` | title, description, category_id, price, condition, attributes, `photos` (local paths, 1-10), shippable, max_weight_kg | `{id, url}`; on a missing required field, those fields with their options (from `upload/components`) | write |

Behaviour notes:
- `edit_item`: GET the item, merge the changes and PUT the full body. Reuse the item's location (`approximated: true`), its picture ids, and `cost_configuration_id` from `shipping`. Description is sent as a plain string.
- `set_item_status` on a 404 within ~10 s of publishing: return "item not ready yet, retry in a few seconds". No retry loop.
- `send_message` dedupes against agent retries: if my last message in the thread has the same text, return `already_sent`.
- `create_listing`: photos are read from disk (jpeg/png, size-capped). Photo 1 goes in the multipart create, the rest through `picture2` with `order`. Shipping uses `BUYER_PAYS`.

## Session handling

- Config: `WALLAPOP_SESSION` (cookie value) seeds the session.
- After each mint, keep the rotated cookie in memory and persist it to `os.UserConfigDir()/wallapop-mcp/session` (0600). On start, use whichever is newer. This keeps the server working indefinitely, as long as it runs at least once every 30 days, without re-pasting the cookie.
- Mint lazily, cache until `exp - 30s`. On a 401, re-mint once and retry. A mint that returns `{}` gives "Wallapop session expired: copy a fresh __Secure-next-auth.session-token into WALLAPOP_SESSION".

## Code changes

- `client.go`: replace GET-only `fetchJSON` with `do(ctx, method, url, header, body, out)` that accepts any 2xx (204 means no decode). `HTTPError` gets `Method`. Add an `authed` path (bearer + one re-mint on 401). Same `gate`.
- `auth.go`: mint, rotate and persist the cookie, decode JWT claims with stdlib, mutex-guarded cache.
- `account.go`: `my_items` (incl. SDUI flattening), `inbox`, `get_conversation`.
- `listing.go`: `edit_item`, `set_item_status`, `create_listing` (stdlib `mime/multipart`).
- `chat.go`: `send_message` via PubNub REST, no SDK.
- `tools.go` / `main.go`: register behind a configured session.
- No new dependencies.

## Tests

- Offline: extend `fake_test.go` with handlers that check method, path, headers and body. Fixtures are scrubbed burner captures in `testdata/auth/`. Phase 0 captures are in the session scratchpad and still need scrubbing.
- Seams: mint (including the `{}` case and cookie rotation/persistence with a temp dir), 401 re-mint, edit merge, `send_message` dedupe, multipart shape and the `picture2` text response, SDUI flattening, the tools/list byte budget.
- Live read (`-tags live`): `my_items` and `inbox` on the burner, skipped without a session.
- Live write (`-tags livewrite`, burner only): publish, then add a photo, check `my_items`, edit, reserve, unreserve, sold and delete. This is the same sequence run by hand in Phase 0.

## Phases

1. **Client, auth, reads:** `do`/`authed`, `auth.go`, `my_items`, `inbox`, `get_conversation` (thread shape verified once a conversation exists), budget test.
2. **Listing management:** `set_item_status`, `edit_item`, `livewrite` lifecycle.
3. **Messaging:** `send_message`. Burner sends to a listing on the main account; the main account replies on demand to verify receiving (`inbox`, `get_conversation`).
4. **Create listing:** multipart publish, extra photos, required-field validation, check the location behaviour.
5. **README:** env vars, tools, cookie extraction steps, ToS/account-risk note.

## Out of scope

Offers, favorites, saved searches, purchases (agreed 2026-11-19). Pause/resume (blocked with 403).

## Sources

- Mergoth/wallapop-mcp `docs/ENDPOINTS.md`: https://github.com/Mergoth/wallapop-mcp
- AdeiTamayo/WallpopMCP: https://github.com/AdeiTamayo/WallpopMCP
- Altitud-Studio-S-L-U/wallapop-tools: https://github.com/Altitud-Studio-S-L-U/wallapop-tools
- Microck/wallapop-cli: https://github.com/Microck/wallapop-cli
- Wallapop web bundles `web-static.wallapop.com/angular/es/*.js` (own-items service, cost configuration ids, PubNub keys), fetched 2026-11-19
