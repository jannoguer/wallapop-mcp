package main

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ItemParams struct {
	ID string `json:"id" jsonschema:"item id, from a search result"`
}

type SellerParams struct {
	ID      string `json:"id" jsonschema:"seller id (seller_id in search results)"`
	Reviews *int   `json:"reviews,omitempty" jsonschema:"how many recent reviews to include, 0-20 (default 5)"`
}

type CategoryParams struct {
	Query string `json:"query,omitempty" jsonschema:"text to match in category names, accents optional; empty lists all"`
}

type CategoryList struct {
	Categories []Category `json:"categories"`
}

func newServer(a *App) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "wallapop", Version: "0.1.0"}, nil)
	ro := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}

	mcp.AddTool(s, &mcp.Tool{
		Name:        "search_items",
		Description: "Search Wallapop second-hand listings (40 per page). Use category_id from list_categories to drop accessories. For ranking candidates prefer search_enriched.",
		Annotations: ro,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in SearchParams) (*mcp.CallToolResult, SearchResult, error) {
		out, err := a.Search(ctx, in)
		return nil, out, err
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "search_enriched",
		Description: "Search Wallapop and attach item details (condition, attributes, full description, views/favorites/conversations) and seller reputation (rating, reviews, sales, reports, account age) to the top results, for picking the best fit in one call.",
		Annotations: ro,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in EnrichParams) (*mcp.CallToolResult, EnrichedResult, error) {
		out, err := a.SearchEnriched(ctx, in)
		return nil, out, err
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_item",
		Description: "Full details of one Wallapop listing.",
		Annotations: ro,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ItemParams) (*mcp.CallToolResult, ItemDetail, error) {
		out, err := a.GetItem(ctx, in.ID)
		return nil, out, err
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_seller",
		Description: "Reputation of a Wallapop seller: rating, review and sales counts, reports received, account age and recent reviews.",
		Annotations: ro,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in SellerParams) (*mcp.CallToolResult, Seller, error) {
		n := defaultReviews
		if in.Reviews != nil {
			n = *in.Reviews
		}
		out, err := a.GetSeller(ctx, in.ID, n)
		return nil, out, err
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_categories",
		Description: "Find Wallapop category ids by name (e.g. \"smartphones\", \"bicicletas\"). Names are in Spanish.",
		Annotations: ro,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in CategoryParams) (*mcp.CallToolResult, CategoryList, error) {
		list, err := a.cats.all(ctx, a.wp)
		if err != nil {
			return nil, CategoryList{}, err
		}
		return nil, CategoryList{Categories: filterCategories(list, in.Query)}, nil
	})

	if a.wp.sess != nil {
		addAccountTools(s, a, ro)
	}
	return s
}

func addAccountTools(s *mcp.Server, a *App, ro *mcp.ToolAnnotations) {
	no := false
	mcp.AddTool(s, &mcp.Tool{
		Name:        "my_items",
		Description: "My own Wallapop listings with views, favorites and chats.",
		Annotations: ro,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in MyItemsParams) (*mcp.CallToolResult, MyItems, error) {
		out, err := a.MyItems(ctx, in)
		return nil, out, err
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "inbox",
		Description: "My Wallapop chats, most recent first, with the last message of each.",
		Annotations: ro,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in InboxParams) (*mcp.CallToolResult, Inbox, error) {
		out, err := a.Inbox(ctx, in)
		return nil, out, err
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_conversation",
		Description: "Messages of one Wallapop chat, oldest first.",
		Annotations: ro,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ConversationParams) (*mcp.CallToolResult, Conversation, error) {
		out, err := a.GetConversation(ctx, in)
		return nil, out, err
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "send_message",
		Description: "Send a Wallapop chat message as me.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: &no},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in SendParams) (*mcp.CallToolResult, SendResult, error) {
		out, err := a.SendMessage(ctx, in)
		return nil, out, err
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "edit_item",
		Description: "Change fields of my listing; omitted fields stay as they are.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: &no, IdempotentHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in EditParams) (*mcp.CallToolResult, Receipt, error) {
		out, err := a.EditItem(ctx, in)
		return nil, out, err
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "set_item_status",
		Description: "Reserve, unreserve, mark sold or delete my listing.",
		Annotations: &mcp.ToolAnnotations{IdempotentHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in StatusParams) (*mcp.CallToolResult, Receipt, error) {
		out, err := a.SetItemStatus(ctx, in)
		return nil, out, err
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "create_listing",
		Description: "Publish a new Wallapop listing with photos from disk.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: &no},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in CreateParams) (*mcp.CallToolResult, Receipt, error) {
		out, err := a.CreateListing(ctx, in)
		return nil, out, err
	})
}
