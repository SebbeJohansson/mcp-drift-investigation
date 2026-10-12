// Package shopmcp is the mock MCP server wrapping the mock shop API.
//
// This is the code the self-healing agent is expected to edit when the
// upstream API drifts. It is written against API v1 with X-API-Key auth.
package shopmcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const apiPrefix = "/v1"

type Config struct {
	BaseURL string
	APIKey  string
	Timeout time.Duration
}

type Product struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Price    float64 `json:"price"`
	Currency string  `json:"currency"`
	Stock    int     `json:"stock"`
	InStock  bool    `json:"in_stock"`
}

type ProductList struct {
	Products []Product `json:"products"`
}

type Order struct {
	ID        string  `json:"id"`
	ProductID string  `json:"product_id"`
	Quantity  int     `json:"quantity"`
	Total     float64 `json:"total"`
	Currency  string  `json:"currency"`
	Status    string  `json:"status"`
}

// apiProduct uses pointers so a missing upstream field is an error, not a silent zero.
type apiProduct struct {
	ID       *string  `json:"id"`
	Name     *string  `json:"name"`
	Price    *float64 `json:"price"`
	Currency *string  `json:"currency"`
	Stock    *int     `json:"stock"`
}

func (p apiProduct) toProduct() (Product, error) {
	missing := []string{}
	for name, v := range map[string]bool{
		"id": p.ID == nil, "name": p.Name == nil, "price": p.Price == nil,
		"currency": p.Currency == nil, "stock": p.Stock == nil,
	} {
		if v {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		return Product{}, fmt.Errorf("upstream product response missing fields %v", missing)
	}
	return Product{
		ID: *p.ID, Name: *p.Name, Price: *p.Price, Currency: *p.Currency,
		Stock: *p.Stock, InStock: *p.Stock > 0,
	}, nil
}

type GetProductInput struct {
	ProductID string `json:"product_id" jsonschema:"the product id"`
}

type CreateOrderInput struct {
	ProductID string `json:"product_id" jsonschema:"the product id"`
	Quantity  int    `json:"quantity" jsonschema:"number of items, at least 1"`
}

type GetOrderInput struct {
	OrderID string `json:"order_id" jsonschema:"the order id"`
}

type client struct {
	cfg  Config
	http *http.Client
}

func (c *client) do(ctx context.Context, method, path string, body, out any) error {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reqBody = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.cfg.BaseURL+apiPrefix+path, reqBody)
	if err != nil {
		return err
	}
	req.Header.Set("X-API-Key", c.cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		var e struct {
			Detail string `json:"detail"`
		}
		if json.Unmarshal(raw, &e) != nil || e.Detail == "" {
			e.Detail = string(raw)
		}
		return fmt.Errorf("%d: %s", resp.StatusCode, e.Detail)
	}
	return json.Unmarshal(raw, out)
}

func New(cfg Config) *mcp.Server {
	c := &client{cfg: cfg, http: &http.Client{Timeout: cfg.Timeout}}
	s := mcp.NewServer(&mcp.Implementation{Name: "mock-shop", Version: "1.0.0"}, nil)

	mcp.AddTool(s, &mcp.Tool{Name: "list_products", Description: "List all products in the shop."},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, ProductList, error) {
			var raw []apiProduct
			if err := c.do(ctx, http.MethodGet, "/products", nil, &raw); err != nil {
				return nil, ProductList{}, err
			}
			out := ProductList{Products: []Product{}}
			for _, r := range raw {
				p, err := r.toProduct()
				if err != nil {
					return nil, ProductList{}, err
				}
				out.Products = append(out.Products, p)
			}
			return nil, out, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "get_product", Description: "Get one product by id."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in GetProductInput) (*mcp.CallToolResult, Product, error) {
			var raw apiProduct
			if err := c.do(ctx, http.MethodGet, "/products/"+in.ProductID, nil, &raw); err != nil {
				return nil, Product{}, err
			}
			p, err := raw.toProduct()
			return nil, p, err
		})

	mcp.AddTool(s, &mcp.Tool{Name: "create_order", Description: "Place an order for a product."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in CreateOrderInput) (*mcp.CallToolResult, Order, error) {
			var o Order
			body := map[string]any{"product_id": in.ProductID, "quantity": in.Quantity}
			err := c.do(ctx, http.MethodPost, "/orders", body, &o)
			return nil, o, err
		})

	mcp.AddTool(s, &mcp.Tool{Name: "get_order", Description: "Get an order by id."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in GetOrderInput) (*mcp.CallToolResult, Order, error) {
			var o Order
			err := c.do(ctx, http.MethodGet, "/orders/"+in.OrderID, nil, &o)
			return nil, o, err
		})

	return s
}
