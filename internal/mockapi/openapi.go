package mockapi

import (
	"net/http"
	"strings"
)

type obj = map[string]any

func ref(name string) obj { return obj{"$ref": "#/components/schemas/" + name} }

func jsonBody(schema obj) obj {
	return obj{"content": obj{"application/json": obj{"schema": schema}}}
}

func response(desc string, schema obj) obj {
	r := jsonBody(schema)
	r["description"] = desc
	return r
}

// Spec builds the published OpenAPI spec for the active chaos switches.
// rotate_key, flaky_5xx and slow do not show up here, like in real life.
func (a *API) Spec() obj {
	c := a.Chaos
	version, prefix := "1.0.0", "/v1"
	if c.On("path_v2") {
		version, prefix = "2.0.0", "/v2"
	}

	notFound, notFoundCode := response("Not found", ref("Error")), "404"
	if c.On("error_format") {
		notFoundCode = "410"
	}

	product := obj{
		"type": "object",
		"properties": obj{
			"id":       obj{"type": "string"},
			"name":     obj{"type": "string"},
			"price":    obj{"type": "number"},
			"currency": obj{"type": "string"},
			"stock":    obj{"type": "integer"},
		},
		"required": []string{"id", "name", "price", "currency", "stock"},
	}
	if c.On("rename_field") {
		props := product["properties"].(obj)
		props["unit_price"] = props["price"]
		delete(props, "price")
		product["required"] = []string{"id", "name", "unit_price", "currency", "stock"}
	}
	if c.On("remove_field") {
		delete(product["properties"].(obj), "stock")
		var req []string
		for _, f := range product["required"].([]string) {
			if f != "stock" {
				req = append(req, f)
			}
		}
		product["required"] = req
	}

	orderCreateRequired := []string{"product_id", "quantity"}
	if c.On("required_param") {
		orderCreateRequired = append(orderCreateRequired, "currency")
	}

	errorSchema := obj{
		"type":       "object",
		"properties": obj{"detail": obj{"type": "string"}},
		"required":   []string{"detail"},
	}
	if c.On("error_format") {
		errorSchema = obj{
			"type":     "object",
			"required": []string{"errors"},
			"properties": obj{"errors": obj{
				"type": "array",
				"items": obj{
					"type":       "object",
					"required":   []string{"code", "message"},
					"properties": obj{"code": obj{"type": "string"}, "message": obj{"type": "string"}},
				},
			}},
		}
	}

	security := obj{"type": "apiKey", "in": "header", "name": "X-API-Key"}
	if c.On("auth_bearer") {
		security = obj{"type": "http", "scheme": "bearer"}
	}

	idParam := []obj{{"name": "id", "in": "path", "required": true, "schema": obj{"type": "string"}}}
	paths := obj{
		"/v1/products": obj{"get": obj{
			"operationId": "listProducts",
			"responses":   obj{"200": response("OK", obj{"type": "array", "items": ref("Product")})},
		}},
		"/v1/products/{id}": obj{"get": obj{
			"operationId": "getProduct",
			"parameters":  idParam,
			"responses":   obj{"200": response("OK", ref("Product")), notFoundCode: notFound},
		}},
		"/v1/orders": obj{"post": obj{
			"operationId": "createOrder",
			"requestBody": jsonBody(ref("OrderCreate")),
			"responses":   obj{"201": response("Created", ref("Order")), notFoundCode: notFound},
		}},
		"/v1/orders/{id}": obj{"get": obj{
			"operationId": "getOrder",
			"parameters":  idParam,
			"responses":   obj{"200": response("OK", ref("Order")), notFoundCode: notFound},
		}},
	}
	versioned := obj{}
	for p, v := range paths {
		versioned[prefix+strings.TrimPrefix(p, "/v1")] = v
	}

	return obj{
		"openapi":  "3.1.0",
		"info":     obj{"title": "Mock Shop API", "version": version},
		"paths":    versioned,
		"security": []obj{{"auth": []string{}}},
		"components": obj{
			"securitySchemes": obj{"auth": security},
			"schemas": obj{
				"Product": product,
				"OrderCreate": obj{
					"type": "object",
					"properties": obj{
						"product_id": obj{"type": "string"},
						"quantity":   obj{"type": "integer", "minimum": 1},
						"currency":   obj{"type": "string"},
					},
					"required": orderCreateRequired,
				},
				"Order": obj{
					"type": "object",
					"properties": obj{
						"id":         obj{"type": "string"},
						"product_id": obj{"type": "string"},
						"quantity":   obj{"type": "integer"},
						"total":      obj{"type": "number"},
						"currency":   obj{"type": "string"},
						"status":     obj{"type": "string"},
					},
					"required": []string{"id", "product_id", "quantity", "total", "currency", "status"},
				},
				"Error": errorSchema,
			},
		},
	}
}

func (a *API) openapi(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, a.Spec())
}
