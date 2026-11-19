package main

import (
	"context"
	"fmt"
	"sync"
)

const (
	defaultTopN       = 10
	maxTopN           = 20
	enrichConcurrency = 4
)

type EnrichParams struct {
	SearchParams
	TopN int `json:"top_n,omitempty" jsonschema:"how many of the first results to enrich, 1-20 (default 10)"`
}

type EnrichedListing struct {
	Listing
	Condition     string            `json:"condition,omitempty"`
	Attributes    map[string]string `json:"attributes,omitempty"`
	Views         int               `json:"views"`
	Favorites     int               `json:"favorites"`
	Conversations int               `json:"conversations"`
	Seller        *Seller           `json:"seller,omitempty"`
	Error         string            `json:"error,omitempty" jsonschema:"why item or seller details are missing for this listing"`
}

type EnrichedResult struct {
	Location   *Place            `json:"location,omitempty" jsonschema:"search origin; absent means Wallapop used the server's IP location"`
	Listings   []EnrichedListing `json:"listings"`
	NextCursor string            `json:"next_cursor,omitempty" jsonschema:"next page of 40 raw results; listings past top_n on this page are skipped"`
}

func (a *App) SearchEnriched(ctx context.Context, p EnrichParams) (EnrichedResult, error) {
	n := p.TopN
	if n == 0 {
		n = defaultTopN
	}
	if n < 1 || n > maxTopN {
		return EnrichedResult{}, fmt.Errorf("top_n must be between 1 and %d", maxTopN)
	}
	res, err := a.Search(ctx, p.SearchParams)
	if err != nil {
		return EnrichedResult{}, err
	}
	listings := res.Listings[:min(n, len(res.Listings))]

	items := make([]ItemDetail, len(listings))
	itemErrs := make([]error, len(listings))
	var mu sync.Mutex
	sellers := map[string]*Seller{}
	sellerErrs := map[string]error{}

	var wg sync.WaitGroup
	sem := make(chan struct{}, enrichConcurrency)
	run := func(f func()) {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			f()
		})
	}
	seen := map[string]bool{}
	for i, l := range listings {
		run(func() { items[i], itemErrs[i] = a.GetItem(ctx, l.ID) })
		if !seen[l.SellerID] {
			seen[l.SellerID] = true
			run(func() {
				s, err := a.GetSeller(ctx, l.SellerID, 0)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					sellerErrs[l.SellerID] = err
					return
				}
				sellers[l.SellerID] = &s
			})
		}
	}
	wg.Wait()

	out := EnrichedResult{Location: res.Location, Listings: []EnrichedListing{}, NextCursor: res.NextCursor}
	for i, l := range listings {
		e := EnrichedListing{Listing: l, Seller: sellers[l.SellerID]}
		if err := itemErrs[i]; err != nil {
			e.Error = "item: " + err.Error()
		} else {
			it := items[i]
			e.Description = it.Description
			e.Condition = it.Condition
			e.Attributes = it.Attributes
			e.Views, e.Favorites, e.Conversations = it.Views, it.Favorites, it.Conversations
		}
		if err := sellerErrs[l.SellerID]; err != nil {
			if e.Error != "" {
				e.Error += "; "
			}
			e.Error += "seller: " + err.Error()
		}
		out.Listings = append(out.Listings, e)
	}
	return out, nil
}
