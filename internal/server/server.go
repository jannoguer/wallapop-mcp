// Package server exposes the Wallapop client as MCP tools.
package server

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jannoguer/wallapop-mcp/internal/wallapop"
)

const (
	version        = "0.1.0"
	defaultReviews = 5
)

type ItemParams struct {
	ID string `json:"id" jsonschema:"item id from a search result, or a listing link or slug"`
}

type SellerParams struct {
	ID      string `json:"id" jsonschema:"seller id (seller_id in search results), or a profile link or slug"`
	Reviews *int   `json:"reviews,omitempty" jsonschema:"how many recent reviews to include, 0-20 (default 5)"`
}

type CategoryParams struct {
	Query string `json:"query,omitempty" jsonschema:"text to match in category names, accents optional; empty lists all"`
}

type CategoryList struct {
	Categories []wallapop.Category `json:"categories"`
}

// New registers the anonymous tools, plus the account tools when c has a session.
func New(c *wallapop.Client) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "wallapop", Version: version}, nil)
	ro := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}

	add(s, "search_items", "Search Wallapop second-hand listings (40 per page). Use category_id from list_categories to drop accessories. For ranking candidates prefer search_enriched.",
		ro, c.Search)
	add(s, "search_enriched", "Search Wallapop and attach item details (condition, attributes, full description, views/favorites/conversations) and seller reputation (rating, reviews, sales, reports, account age) to the top results, for picking the best fit in one call.",
		ro, c.SearchEnriched)
	add(s, "get_item", "Full details of one Wallapop listing, with shipping options and costs.",
		ro, func(ctx context.Context, in ItemParams) (wallapop.ItemDetail, error) {
			return c.Item(ctx, in.ID)
		})
	add(s, "get_seller", "Reputation of a Wallapop seller: rating, review, sales and shipment counts, reports received, account age and recent reviews.",
		ro, func(ctx context.Context, in SellerParams) (wallapop.Seller, error) {
			n := defaultReviews
			if in.Reviews != nil {
				n = *in.Reviews
			}
			return c.Seller(ctx, in.ID, n)
		})
	add(s, "list_categories", `Find Wallapop category ids by name (e.g. "smartphones", "bicicletas"). Names are in Spanish.`,
		ro, func(ctx context.Context, in CategoryParams) (CategoryList, error) {
			list, err := c.Categories(ctx, in.Query)
			return CategoryList{Categories: list}, err
		})
	if !c.HasSession() {
		return s
	}

	no := false
	add(s, "my_items", "My own Wallapop listings with views, favorites and chats.", ro, c.MyItems)
	add(s, "inbox", "My Wallapop chats, most recent first, with the last message of each.", ro, c.Inbox)
	add(s, "get_conversation", "Messages of one Wallapop chat, oldest first.", ro, c.Conversation)
	add(s, "send_message", "Send a Wallapop chat message as me.",
		&mcp.ToolAnnotations{DestructiveHint: &no}, c.SendMessage)
	add(s, "edit_item", "Change fields of my listing; omitted fields stay as they are.",
		&mcp.ToolAnnotations{DestructiveHint: &no, IdempotentHint: true}, c.EditItem)
	add(s, "set_item_status", "Reserve, unreserve, mark sold or delete my listing.",
		&mcp.ToolAnnotations{IdempotentHint: true}, c.SetItemStatus)
	add(s, "create_listing", "Publish a new Wallapop listing with photos from disk.",
		&mcp.ToolAnnotations{DestructiveHint: &no}, c.CreateListing)
	return s
}

// add registers f as a tool with typed input and output. The SDK derives
// both schemas from In and Out and turns an error into a tool error.
func add[In, Out any](s *mcp.Server, name, desc string, ann *mcp.ToolAnnotations, f func(context.Context, In) (Out, error)) {
	mcp.AddTool(s, &mcp.Tool{Name: name, Description: desc, Annotations: ann},
		func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
			out, err := f(ctx, in)
			return nil, out, err
		})
}
