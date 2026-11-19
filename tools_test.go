package main

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func connect(t *testing.T, a *App) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	if _, err := newServer(a).Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any, out any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if out != nil && !res.IsError {
		b, _ := json.Marshal(res.StructuredContent)
		if err := json.Unmarshal(b, out); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	return res
}

func errorText(res *mcp.CallToolResult) string {
	if !res.IsError || len(res.Content) == 0 {
		return ""
	}
	return res.Content[0].(*mcp.TextContent).Text
}

func TestListTools(t *testing.T) {
	cs := connect(t, newFake(t).app(t, ""))
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("%s is not marked read-only", tool.Name)
		}
		if tool.OutputSchema == nil {
			t.Errorf("%s has no output schema", tool.Name)
		}
		if tool.Name == "search_enriched" {
			schema, _ := json.Marshal(tool.InputSchema)
			for _, field := range []string{`"query"`, `"city"`, `"category_id"`, `"top_n"`} {
				if !strings.Contains(string(schema), field) {
					t.Errorf("search_enriched schema lacks %s: %s", field, schema)
				}
			}
		}
	}
	slices.Sort(names)
	want := []string{"get_item", "get_seller", "list_categories", "search_enriched", "search_items"}
	if !slices.Equal(names, want) {
		t.Errorf("tools = %v", names)
	}
}

func TestToolCalls(t *testing.T) {
	f := newFake(t)
	cs := connect(t, f.app(t, ""))

	var sr SearchResult
	call(t, cs, "search_items", map[string]any{"query": "iphone", "city": "Girona", "conditions": []string{"good"}}, &sr)
	if len(sr.Listings) != 5 || sr.Location == nil {
		t.Errorf("search_items: %d listings, location %v", len(sr.Listings), sr.Location)
	}

	var er EnrichedResult
	call(t, cs, "search_enriched", map[string]any{"query": "iphone", "top_n": 2}, &er)
	if len(er.Listings) != 2 || er.Listings[0].Seller == nil {
		t.Errorf("search_enriched: %+v", er.Listings)
	}

	var it ItemDetail
	call(t, cs, "get_item", map[string]any{"id": "ejkml3e23yzx"}, &it)
	if it.Condition != "fair" {
		t.Errorf("get_item: %+v", it)
	}

	var s Seller
	call(t, cs, "get_seller", map[string]any{"id": "w67dkw9py56x"}, &s)
	if len(s.RecentReviews) != 2 { // default 5, fixture has 2
		t.Errorf("get_seller default reviews: %d", len(s.RecentReviews))
	}
	var s0 Seller
	call(t, cs, "get_seller", map[string]any{"id": "w67dkw9py56x", "reviews": 0}, &s0)
	if s0.ID == "" || len(s0.RecentReviews) != 0 {
		t.Errorf("get_seller reviews=0: %+v", s0)
	}

	var cl CategoryList
	call(t, cs, "list_categories", map[string]any{"query": "smartphones"}, &cl)
	if len(cl.Categories) != 1 || cl.Categories[0].ID != 9447 {
		t.Errorf("list_categories: %+v", cl)
	}
	call(t, cs, "list_categories", map[string]any{"query": "zzz"}, &cl)
	if cl.Categories == nil || len(cl.Categories) != 0 {
		t.Errorf("list_categories no match: %#v", cl.Categories)
	}
}

func TestToolErrors(t *testing.T) {
	f := newFake(t)
	f.wp["GET /items/{id}"] = http.NotFound
	cs := connect(t, f.app(t, ""))

	tests := []struct {
		tool    string
		args    map[string]any
		wantErr string
	}{
		{"search_items", map[string]any{"sort": "cheapest"}, `invalid sort "cheapest"`},
		{"search_items", map[string]any{"city": "Atlantis"}, `"Atlantis" not found`},
		{"search_enriched", map[string]any{"top_n": 50}, "top_n must be between 1 and 20"},
		{"get_item", map[string]any{"id": "nope"}, "HTTP 404"},
		{"get_item", map[string]any{}, "id"},
		{"get_seller", map[string]any{"id": "u", "reviews": 99}, "reviews must be between 0 and 20"},
		{"search_items", map[string]any{"max_price": "cheap"}, "max_price"},
	}
	for _, tt := range tests {
		res := call(t, cs, tt.tool, tt.args, nil)
		if msg := errorText(res); !strings.Contains(msg, tt.wantErr) {
			t.Errorf("%s %v: error %q, want %q", tt.tool, tt.args, msg, tt.wantErr)
		}
	}
}

// modelFacingBudget caps the bytes of tool names, descriptions and input
// schemas: what a client puts in the model's context on every turn.
const modelFacingBudget = 7500

func TestListAccountTools(t *testing.T) {
	res, err := connect(t, newFake(t).authApp(t)).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	writes := map[string]bool{"send_message": true, "edit_item": true, "set_item_status": true, "create_listing": true}
	var names []string
	size := 0
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
		schema, _ := json.Marshal(tool.InputSchema)
		size += len(tool.Name) + len(tool.Description) + len(schema)
		if writes[tool.Name] == tool.Annotations.ReadOnlyHint {
			t.Errorf("%s: read-only hint %v", tool.Name, tool.Annotations.ReadOnlyHint)
		}
	}
	slices.Sort(names)
	want := []string{"create_listing", "edit_item", "get_conversation", "get_item", "get_seller", "inbox",
		"list_categories", "my_items", "search_enriched", "search_items", "send_message", "set_item_status"}
	if !slices.Equal(names, want) {
		t.Errorf("tools = %v", names)
	}
	if size > modelFacingBudget {
		t.Errorf("tool definitions take %d bytes, budget %d: trim descriptions or merge tools", size, modelFacingBudget)
	}
	t.Logf("model-facing tool definitions: %d bytes", size)
}
