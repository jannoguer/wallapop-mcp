package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

type Category struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Path   string `json:"path" jsonschema:"full path from the top-level category"`
	RootID int    `json:"root_id" jsonschema:"top-level category this one belongs to"`
}

type categoryNode struct {
	ID            int            `json:"id"`
	Name          string         `json:"name"`
	Subcategories []categoryNode `json:"subcategories"`
}

type categoryCache struct {
	mu   sync.Mutex
	list []Category
}

func (c *categoryCache) all(ctx context.Context, wp *Client) ([]Category, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.list != nil {
		return c.list, nil
	}
	var res struct {
		Categories []categoryNode `json:"categories"`
	}
	if err := wp.get(ctx, "/categories", nil, &res); err != nil {
		return nil, err
	}
	c.list = flattenCategories(res.Categories)
	return c.list, nil
}

func flattenCategories(roots []categoryNode) []Category {
	out := []Category{}
	var walk func(n categoryNode, path string, root int)
	walk = func(n categoryNode, path string, root int) {
		if path != "" {
			path += " > "
		}
		path += n.Name
		out = append(out, Category{ID: n.ID, Name: n.Name, Path: path, RootID: root})
		for _, s := range n.Subcategories {
			walk(s, path, root)
		}
	}
	for _, r := range roots {
		walk(r, "", r.ID)
	}
	return out
}

func filterCategories(list []Category, query string) []Category {
	q := fold(query)
	out := []Category{}
	for _, c := range list {
		if strings.Contains(fold(c.Name), q) {
			out = append(out, c)
		}
	}
	return out
}

func findCategory(list []Category, id int) (Category, error) {
	for _, c := range list {
		if c.ID == id {
			return c, nil
		}
	}
	return Category{}, fmt.Errorf("unknown category_id %d (use list_categories)", id)
}

// Lets "telefonia" match "Telefonía".
var accentFolder = strings.NewReplacer(
	"á", "a", "à", "a", "â", "a", "ä", "a", "ã", "a",
	"é", "e", "è", "e", "ê", "e", "ë", "e",
	"í", "i", "ì", "i", "î", "i", "ï", "i",
	"ó", "o", "ò", "o", "ô", "o", "ö", "o", "õ", "o",
	"ú", "u", "ù", "u", "û", "u", "ü", "u",
	"ç", "c", "ñ", "n",
)

func fold(s string) string {
	return accentFolder.Replace(strings.ToLower(strings.TrimSpace(s)))
}
