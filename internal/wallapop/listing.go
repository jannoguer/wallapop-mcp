package wallapop

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/jannoguer/wallapop-mcp/internal/httpx"
)

const (
	// Shipping cost configurations, constants in Wallapop's web bundle.
	costBuyerPays = "814429d6-7844-471d-97af-196cd4020f26"
	maxPhotos     = 10
	maxPhotoBytes = 10 << 20
	// Edit, publish and photo upload pick their handler by this Accept value; without it the API answers 404/405.
	acceptUploadV2 = "application/vnd.upload-v2+json"
)

type Receipt struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Note   string `json:"note,omitempty"`
}

type StatusParams struct {
	ID     string `json:"id" jsonschema:"listing id, from my_items"`
	Action string `json:"action" jsonschema:"reserve, unreserve, sold (irreversible) or delete (permanent)"`
}

func (c *Client) SetItemStatus(ctx context.Context, p StatusParams) (Receipt, error) {
	if p.ID == "" {
		return Receipt{}, errors.New("id is required")
	}
	base := c.BaseURL + "/items/" + url.PathEscape(p.ID)
	var method, u, status string
	var body any
	switch p.Action {
	case "reserve", "unreserve":
		method, u, body = http.MethodPut, base+"/reserve", map[string]bool{"reserved": p.Action == "reserve"}
		status = map[string]string{"reserve": "reserved", "unreserve": "active"}[p.Action]
	case "sold":
		method, u, body, status = http.MethodPut, base+"/sold", struct{}{}, "sold"
	case "delete":
		method, u, status = http.MethodDelete, base, "deleted"
	default:
		return Receipt{}, fmt.Errorf("invalid action %q: want reserve, unreserve, sold or delete", p.Action)
	}
	if _, err := c.authedJSON(ctx, method, u, body, nil); err != nil {
		return Receipt{}, notReady(err, p.ID)
	}
	return Receipt{ID: p.ID, Status: status}, nil
}

// notReady explains the 403/404 Wallapop returns for a listing changed within
// a few seconds of being published, before it reaches every service.
func notReady(err error, id string) error {
	var he *httpx.Error
	if errors.As(err, &he) && (he.Status == http.StatusForbidden || he.Status == http.StatusNotFound) {
		return fmt.Errorf("listing %s refused (HTTP %d): not mine, or published seconds ago and not ready yet; retry in a few seconds", id, he.Status)
	}
	return err
}

// itemBody is what the edit (PUT) and publish (POST) endpoints take.
type itemBody struct {
	Attributes     map[string]any `json:"attributes"`
	CategoryLeafID string         `json:"category_leaf_id"`
	ApplyDiscount  bool           `json:"apply_discount"`
	Location       *bodyLocation  `json:"location,omitempty"`
	Delivery       bodyDelivery   `json:"delivery"`
	Pictures       []bodyPicture  `json:"pictures,omitempty"`
	UploadID       string         `json:"upload_id,omitempty"`
}

type bodyLocation struct {
	Latitude     float64 `json:"latitude"`
	Longitude    float64 `json:"longitude"`
	Approximated bool    `json:"approximated"`
}

type bodyDelivery struct {
	AllowedByUser       bool     `json:"allowed_by_user"`
	MaxWeightKg         *float64 `json:"max_weight_kg"`
	CostConfigurationID string   `json:"cost_configuration_id"`
}

type bodyPicture struct {
	ID    string `json:"id"`
	Order int    `json:"order"`
}

type ownItem struct {
	Title       rawText `json:"title"`
	Description rawText `json:"description"`
	Price       struct {
		Cash rawPrice `json:"cash"`
	} `json:"price"`
	Taxonomy []struct {
		ID string `json:"id"`
	} `json:"taxonomy"`
	TypeAttributes map[string]struct {
		Value string `json:"value"`
	} `json:"type_attributes"`
	Location struct {
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
	} `json:"location"`
	Shipping struct {
		UserAllowsShipping  bool   `json:"user_allows_shipping"`
		CostConfigurationID string `json:"cost_configuration_id"`
	} `json:"shipping"`
	Images []struct {
		ID string `json:"id"`
	} `json:"images"`
}

type EditParams struct {
	ID          string            `json:"id" jsonschema:"listing id, from my_items"`
	Title       *string           `json:"title,omitempty"`
	Description *string           `json:"description,omitempty"`
	Price       *float64          `json:"price,omitempty"`
	Condition   *string           `json:"condition,omitempty"`
	CategoryID  *int              `json:"category_id,omitempty" jsonschema:"move to this leaf category"`
	Attributes  map[string]string `json:"attributes,omitempty" jsonschema:"category attributes to set, e.g. brand, model, color"`
	Shippable   *bool             `json:"shippable,omitempty"`
	MaxWeightKg *float64          `json:"max_weight_kg,omitempty" jsonschema:"shipping weight tier in kg"`
	AddPhotos   []string          `json:"add_photos,omitempty" jsonschema:"local image paths to append"`
}

// EditItem changes only the given fields: the API needs the full listing, so
// it reads the current one and sends it back merged.
func (c *Client) EditItem(ctx context.Context, p EditParams) (Receipt, error) {
	if p.ID == "" {
		return Receipt{}, errors.New("id is required")
	}
	photos, err := readPhotos(p.AddPhotos, maxPhotos)
	if err != nil {
		return Receipt{}, err
	}
	itemURL := c.BaseURL + "/items/" + url.PathEscape(p.ID)
	var it ownItem
	if _, err := c.authed(ctx, http.MethodGet, itemURL, nil, nil, &it); err != nil {
		return Receipt{}, err
	}
	if len(it.Taxonomy) == 0 {
		return Receipt{}, fmt.Errorf("listing %s has no category", p.ID)
	}
	if len(it.Images)+len(photos) > maxPhotos {
		return Receipt{}, fmt.Errorf("a listing holds at most %d photos; it has %d", maxPhotos, len(it.Images))
	}

	attrs := map[string]any{
		"title":        it.Title.Original,
		"description":  it.Description.Original,
		"price_amount": it.Price.Cash.Amount,
	}
	var weight *float64
	for k, v := range it.TypeAttributes {
		if k == "up_to_kg" { // the shipping tier travels as delivery.max_weight_kg
			if w, err := strconv.ParseFloat(v.Value, 64); err == nil {
				weight = &w
			}
			continue
		}
		attrs[k] = v.Value
	}
	for k, v := range p.Attributes {
		attrs[k] = v
	}
	if p.Title != nil {
		attrs["title"] = *p.Title
	}
	if p.Description != nil {
		attrs["description"] = *p.Description
	}
	if p.Price != nil {
		attrs["price_amount"] = *p.Price
	}
	if p.Condition != nil {
		attrs["condition"] = *p.Condition
	}
	cat := it.Taxonomy[len(it.Taxonomy)-1].ID
	if p.CategoryID != nil {
		cat = strconv.Itoa(*p.CategoryID)
	}
	ship := it.Shipping.UserAllowsShipping
	if p.Shippable != nil {
		ship = *p.Shippable
	}
	if p.MaxWeightKg != nil {
		weight = p.MaxWeightKg
	}
	if ship && weight == nil {
		return Receipt{}, errors.New("max_weight_kg is required to enable shipping")
	}
	cost := it.Shipping.CostConfigurationID
	if cost == "" {
		cost = costBuyerPays
	}
	body := itemBody{
		Attributes:     attrs,
		CategoryLeafID: cat,
		// Re-sending the listing's own (approximated) location keeps it unchanged.
		Location: &bodyLocation{Latitude: it.Location.Latitude, Longitude: it.Location.Longitude, Approximated: true},
		Delivery: bodyDelivery{AllowedByUser: ship, MaxWeightKg: weight, CostConfigurationID: cost},
	}
	for i, img := range it.Images {
		body.Pictures = append(body.Pictures, bodyPicture{ID: img.ID, Order: i})
	}
	b, err := json.Marshal(body)
	if err != nil {
		return Receipt{}, err
	}
	h := http.Header{"Content-Type": {"application/json"}, "Accept": {acceptUploadV2}}
	if _, err := c.authed(ctx, http.MethodPut, itemURL, h, b, nil); err != nil {
		return Receipt{}, notReady(err, p.ID)
	}
	if err := c.addPhotos(ctx, p.ID, photos, len(it.Images)); err != nil {
		return Receipt{}, err
	}
	return Receipt{ID: p.ID, Status: "updated"}, nil
}

type CreateParams struct {
	Title       string            `json:"title" jsonschema:"max 50 chars, no links"`
	Description string            `json:"description" jsonschema:"max 640 chars"`
	CategoryID  int               `json:"category_id" jsonschema:"leaf category id from list_categories"`
	Price       float64           `json:"price" jsonschema:"EUR"`
	Condition   string            `json:"condition" jsonschema:"un_opened, new, as_good_as_new, good, fair or has_given_it_all"`
	Attributes  map[string]string `json:"attributes,omitempty" jsonschema:"category attributes such as brand, model, color; an error lists missing required ones"`
	Photos      []string          `json:"photos" jsonschema:"1-10 local jpeg/png paths; the first is the cover"`
	Shippable   bool              `json:"shippable,omitempty"`
	MaxWeightKg float64           `json:"max_weight_kg,omitempty" jsonschema:"shipping weight tier in kg, required when shippable"`
}

type photo struct {
	name, mime string
	data       []byte
}

func readPhotos(paths []string, limit int) ([]photo, error) {
	if len(paths) > limit {
		return nil, fmt.Errorf("at most %d photos", limit)
	}
	var out []photo
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		if len(b) > maxPhotoBytes {
			return nil, fmt.Errorf("%s: larger than %d MB", p, maxPhotoBytes>>20)
		}
		mime := http.DetectContentType(b)
		if mime != "image/jpeg" && mime != "image/png" {
			return nil, fmt.Errorf("%s: %s is not a jpeg or png image", p, mime)
		}
		out = append(out, photo{name: filepath.Base(p), mime: mime, data: b})
	}
	return out, nil
}

// component is one field of Wallapop's listing form.
type component struct {
	ID         string `json:"id"`
	IsRequired bool   `json:"is_required"`
	Data       struct {
		Source struct {
			Options []struct {
				ID string `json:"id"`
			} `json:"options"`
		} `json:"source"`
	} `json:"data"`
}

func (c *Client) CreateListing(ctx context.Context, p CreateParams) (Receipt, error) {
	switch {
	case p.Title == "" || p.Description == "" || p.Condition == "":
		return Receipt{}, errors.New("title, description and condition are required")
	case p.CategoryID == 0:
		return Receipt{}, errors.New("category_id is required")
	case p.Price <= 0:
		return Receipt{}, errors.New("price must be positive")
	case len(p.Photos) == 0:
		return Receipt{}, errors.New("at least one photo is required")
	case p.Shippable && p.MaxWeightKg <= 0:
		return Receipt{}, errors.New("max_weight_kg is required when shippable")
	}
	photos, err := readPhotos(p.Photos, maxPhotos)
	if err != nil {
		return Receipt{}, err
	}
	cats, err := c.categories(ctx)
	if err != nil {
		return Receipt{}, err
	}
	cat, err := findCategory(cats, p.CategoryID)
	if err != nil {
		return Receipt{}, err
	}
	uploadID, err := newUUID()
	if err != nil {
		return Receipt{}, err
	}

	fields := map[string]string{
		"title": p.Title, "description": p.Description, "condition": p.Condition,
		"price_amount": formatFloat(p.Price),
	}
	for k, v := range p.Attributes {
		fields[k] = v
	}
	if err := c.checkRequired(ctx, cat, uploadID, fields); err != nil {
		return Receipt{}, err
	}

	attrs := map[string]any{}
	for k, v := range fields {
		attrs[k] = v
	}
	attrs["price_amount"] = p.Price
	body := itemBody{
		Attributes:     attrs,
		CategoryLeafID: strconv.Itoa(p.CategoryID),
		// No location: Wallapop places the listing at the account's location.
		Delivery: bodyDelivery{AllowedByUser: p.Shippable, CostConfigurationID: costBuyerPays},
		UploadID: uploadID,
	}
	if p.Shippable {
		body.Delivery.MaxWeightKg = &p.MaxWeightKg
	}
	item, err := json.Marshal(body)
	if err != nil {
		return Receipt{}, err
	}
	mp, ctype, err := multipartBody(photos[0], map[string]string{"item": string(item)})
	if err != nil {
		return Receipt{}, err
	}
	var res struct {
		ID string `json:"id"`
	}
	h := http.Header{"Content-Type": {ctype}, "Accept": {acceptUploadV2}}
	if _, err := c.authed(ctx, http.MethodPost, c.BaseURL+"/items", h, mp, &res); err != nil {
		return Receipt{}, err
	}
	if res.ID == "" {
		return Receipt{}, errors.New("publish returned no listing id")
	}
	if err := c.addPhotos(ctx, res.ID, photos[1:], 1); err != nil {
		return Receipt{ID: res.ID, Status: "published", Note: "some photos failed: " + err.Error()}, nil
	}
	return Receipt{ID: res.ID, Status: "published"}, nil
}

// checkRequired asks Wallapop's listing form which fields this category
// requires and reports the missing ones with their allowed values.
func (c *Client) checkRequired(ctx context.Context, cat Category, uploadID string, fields map[string]string) error {
	form := map[string]string{
		"summary": fields["title"], "category_leaf_id": strconv.Itoa(cat.ID), "root_category_id": strconv.Itoa(cat.RootID),
	}
	for k, v := range fields {
		form[k] = v
	}
	req := map[string]any{"fields": form, "mode": map[string]string{"action": "upload", "id": uploadID}}
	var res struct {
		Components []component `json:"components"`
	}
	if _, err := c.authedJSON(ctx, http.MethodPost, c.BaseURL+"/items/upload/components", req, &res); err != nil {
		return err
	}
	var missing []string
	for _, comp := range res.Components {
		if !comp.IsRequired || comp.ID == "photo" || fields[comp.ID] != "" {
			continue
		}
		m := comp.ID
		if len(comp.Data.Source.Options) > 0 {
			var ids []string
			for _, o := range comp.Data.Source.Options {
				ids = append(ids, o.ID)
			}
			m += " (" + strings.Join(ids, ", ") + ")"
		}
		missing = append(missing, m)
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		return fmt.Errorf("missing required attributes: %s", strings.Join(missing, "; "))
	}
	return nil
}

// addPhotos appends photos to a listing, one request each.
func (c *Client) addPhotos(ctx context.Context, id string, photos []photo, firstOrder int) error {
	for i, ph := range photos {
		b, ctype, err := multipartBody(ph, map[string]string{"order": strconv.Itoa(firstOrder + i)})
		if err != nil {
			return err
		}
		h := http.Header{"Content-Type": {ctype}, "Accept": {acceptUploadV2}}
		u := c.BaseURL + "/items/" + url.PathEscape(id) + "/picture2"
		if _, err := c.authed(ctx, http.MethodPost, u, h, b, nil); err != nil {
			return fmt.Errorf("photo %s: %w", ph.name, err)
		}
	}
	return nil
}

func multipartBody(ph photo, fields map[string]string) ([]byte, string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	hdr := textproto.MIMEHeader{}
	hdr.Set("Content-Disposition", fmt.Sprintf(`form-data; name="image"; filename=%q`, ph.name))
	hdr.Set("Content-Type", ph.mime)
	part, err := w.CreatePart(hdr)
	if err != nil {
		return nil, "", err
	}
	if _, err := part.Write(ph.data); err != nil {
		return nil, "", err
	}
	for _, k := range slices.Sorted(maps.Keys(fields)) {
		if err := w.WriteField(k, fields[k]); err != nil {
			return nil, "", err
		}
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), w.FormDataContentType(), nil
}

func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}
