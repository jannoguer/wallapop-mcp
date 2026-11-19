# Wallapop private API notes

What the client relies on, as observed on a burner account (2026-11-19). "Verified" means it ran against the real API; the live tests (`go test -tags live`, `-tags "live livewrite"`) re-check it.

## Behaviour found while building the client
- Edits and status changes on a listing published less than ~5 s ago get 403 (edit) or 404 (reserve). The tools report "not ready yet, retry".
- Publishing needs no `location` (the listing goes to the account's location). Decimal prices work.
- `X-DeviceID` is not needed. An early 403 that looked like a missing header was the timing issue above.
- Real conversation fields: `hash`, `unread_messages`, `item.hash`, `item.title`, `with_user.name`, `with_user.hash`, `channel` (`chat.<other>.<conv>.<me>`); messages carry `from_self`, `text`, `type`, `status`, `timestamp` (epoch ms), newest first. The third-party names (`unread_counter`, `item.id`, `micro_name`) were wrong.
- Reading a thread does not mark it read. The inbox has no pagination header, so `inbox` takes `limit` only.

## Auth (verified)
- The long-lived credential is the cookie `__Secure-next-auth.session-token` from `es.wallapop.com`. `GET https://es.wallapop.com/api/auth/session` with only that cookie returns `{"token": "<JWT>", ...}`.
- An invalid or expired cookie still gets HTTP 200, but with body `{}`. Treat a missing `token` as "session expired".
- The access JWT lives **5 minutes**. Claims include `hashed_sub` (my user hash, needed for chat) and `exp`.
- Every mint returns a fresh session cookie in `Set-Cookie`, with a sliding 30-day expiry. The previous cookie keeps working, so there is no reuse revocation.
- **Only `Authorization: Bearer` is required.** No `X-AppVersion`, `X-DeviceID`, `Origin` or browser User-Agent (Go's default UA works). The existing `X-DeviceOS: 0` stays because the anonymous CDN needs it.
- No request signing (none in the web bundles, and writes work without it).

## Endpoints (host `https://api.wallapop.com`)
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
- The listing's location did not follow the coordinates sent (Barcelona sent, an approximated Banyoles shown), so publishing omits `location`.
- On edit, re-sending the item's own approximated location (`approximated: true`) was accepted and left the location unchanged.

## Design rules for low token cost

1. **Few tools, one sentence each.** Every tool schema is sent on every turn; `internal/server` tests cap their total size.
2. **Compact output.** `omitempty` everywhere. Dates as `2006-01-02`. No image URLs, raw objects or SDUI noise. Message text truncated to 160 chars in lists.
3. **Writes return a receipt** (`{"id":..,"status":"reserved"}`), never a re-fetch.
4. **Round trips:** inbox uses `max_messages=1`. `get_conversation` defaults to 20 messages. The JWT (5 min), IM token and user hash are cached per process.
5. The go-sdk sends typed results as both `structuredContent` and JSON text (go-sdk `server.go:468`), so result structs are the real cost: keep them flat.

## Out of scope

Offers, favorites, saved searches, purchases (agreed 2026-11-19). Pause/resume (blocked with 403).

## Sources

- Mergoth/wallapop-mcp `docs/ENDPOINTS.md`: https://github.com/Mergoth/wallapop-mcp
- AdeiTamayo/WallpopMCP: https://github.com/AdeiTamayo/WallpopMCP
- Altitud-Studio-S-L-U/wallapop-tools: https://github.com/Altitud-Studio-S-L-U/wallapop-tools
- Microck/wallapop-cli: https://github.com/Microck/wallapop-cli
- Wallapop web bundles `web-static.wallapop.com/angular/es/*.js` (own-items service, cost configuration ids, PubNub keys), fetched 2026-11-19
