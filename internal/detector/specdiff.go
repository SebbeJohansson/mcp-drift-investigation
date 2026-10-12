package detector

import (
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
)

type obj = map[string]any

// Change is one difference between the baseline and the current state.
type Change struct {
	Source   string `json:"source"` // openapi | tools | contracts | health
	Kind     string `json:"kind"`
	Location string `json:"location"`
	Detail   string `json:"detail"`
	Breaking bool   `json:"breaking"`
}

var httpMethods = []string{"get", "put", "post", "delete", "patch", "head", "options"}

// DiffSpecs compares two OpenAPI documents and classifies each change.
//
// Breaking: removed operations or response codes, removed or retyped
// response fields, fields that stop being required in responses, new
// required request fields, and changed security schemes.
func DiffSpecs(base, cur obj) []Change {
	var out []Change
	add := func(kind, loc, detail string, breaking bool) {
		out = append(out, Change{Source: "openapi", Kind: kind, Location: loc, Detail: detail, Breaking: breaking})
	}

	bv, cv := str(base, "info", "version"), str(cur, "info", "version")
	if bv != cv {
		add("version_changed", "info.version", fmt.Sprintf("%s -> %s", bv, cv), false)
	}

	// Operations and response codes.
	bOps, cOps := operations(base), operations(cur)
	for _, key := range sortedKeys(bOps) {
		cop, ok := cOps[key]
		if !ok {
			detail := "operation removed"
			if moved := findByOperationID(cOps, opID(bOps[key])); moved != "" {
				detail = fmt.Sprintf("operation %s moved to %s", opID(bOps[key]), moved)
			}
			add("operation_removed", key, detail, true)
			continue
		}
		bCodes, cCodes := sortedKeys(get(bOps[key], "responses")), sortedKeys(get(cop, "responses"))
		for _, code := range bCodes {
			if !slices.Contains(cCodes, code) {
				add("response_code_removed", key, fmt.Sprintf("response %s removed (now %v)", code, cCodes), true)
			}
		}
		for _, code := range cCodes {
			if !slices.Contains(bCodes, code) {
				add("response_code_added", key, "response "+code+" added", false)
			}
		}
	}
	for _, key := range sortedKeys(cOps) {
		if _, ok := bOps[key]; !ok {
			add("operation_added", key, "operation added", false)
		}
	}

	// Component schemas, judged by whether they are sent (request) or received (response).
	reqUse, respUse := schemaUsage(base)
	bSchemas, cSchemas := get(base, "components", "schemas"), get(cur, "components", "schemas")
	for _, name := range sortedKeys(bSchemas) {
		loc := "#/components/schemas/" + name
		bs, _ := bSchemas[name].(obj)
		cs, ok := cSchemas[name].(obj)
		if !ok {
			add("schema_removed", loc, "schema removed", true)
			continue
		}
		bProps, cProps := get(bs, "properties"), get(cs, "properties")
		bReq, cReq := strs(bs["required"]), strs(cs["required"])
		var removed, added []string
		for _, p := range sortedKeys(bProps) {
			cp, ok := cProps[p]
			if !ok {
				removed = append(removed, p)
				add("property_removed", loc+"."+p, "property removed", respUse[name])
				continue
			}
			if bt, ct := str(bProps[p].(obj), "type"), str(cp.(obj), "type"); bt != ct {
				add("property_type_changed", loc+"."+p, fmt.Sprintf("type %s -> %s", bt, ct), true)
			}
		}
		for _, p := range sortedKeys(cProps) {
			if _, ok := bProps[p]; !ok {
				added = append(added, p)
				add("property_added", loc+"."+p, "property added", false)
			}
		}
		for _, r := range cReq {
			if !slices.Contains(bReq, r) {
				add("required_added", loc+"."+r, "field became required", reqUse[name])
			}
		}
		for _, r := range bReq {
			if !slices.Contains(cReq, r) && !slices.Contains(removed, r) {
				add("required_removed", loc+"."+r, "field no longer required", respUse[name])
			}
		}
		if len(removed) == 1 && len(added) == 1 {
			add("possible_rename", loc, fmt.Sprintf("%s looks renamed to %s", removed[0], added[0]), respUse[name])
		}
	}

	// Security schemes.
	bSec, cSec := get(base, "components", "securitySchemes"), get(cur, "components", "securitySchemes")
	if !reflect.DeepEqual(bSec, cSec) {
		add("security_changed", "#/components/securitySchemes",
			fmt.Sprintf("%s -> %s", describeSecurity(bSec), describeSecurity(cSec)), true)
	}
	return out
}

// operations maps "METHOD /path" to the operation object.
func operations(spec obj) map[string]obj {
	out := map[string]obj{}
	for path, item := range get(spec, "paths") {
		m, _ := item.(obj)
		for _, method := range httpMethods {
			if op, ok := m[method].(obj); ok {
				out[strings.ToUpper(method)+" "+path] = op
			}
		}
	}
	return out
}

func opID(op obj) string { s, _ := op["operationId"].(string); return s }

func findByOperationID(ops map[string]obj, id string) string {
	if id == "" {
		return ""
	}
	for key, op := range ops {
		if opID(op) == id {
			return key
		}
	}
	return ""
}

// schemaUsage reports which component schemas are referenced from request
// bodies and which from responses, following nested $refs.
func schemaUsage(spec obj) (req, resp map[string]bool) {
	req, resp = map[string]bool{}, map[string]bool{}
	schemas := get(spec, "components", "schemas")
	var mark func(v any, into map[string]bool)
	mark = func(v any, into map[string]bool) {
		switch t := v.(type) {
		case obj:
			if r, ok := t["$ref"].(string); ok {
				name := strings.TrimPrefix(r, "#/components/schemas/")
				if !into[name] {
					into[name] = true
					mark(schemas[name], into)
				}
			}
			for _, child := range t {
				mark(child, into)
			}
		case []any:
			for _, child := range t {
				mark(child, into)
			}
		}
	}
	for _, op := range operations(spec) {
		mark(op["requestBody"], req)
		mark(op["responses"], resp)
	}
	return req, resp
}

func describeSecurity(s obj) string {
	var parts []string
	for _, name := range sortedKeys(s) {
		m, _ := s[name].(obj)
		parts = append(parts, fmt.Sprintf("%s(%v %v %v)", name, m["type"], m["scheme"], m["name"]))
	}
	return strings.Join(parts, ",")
}

// get walks nested objects; missing keys give an empty object.
func get(m obj, path ...string) obj {
	cur := m
	for _, p := range path {
		next, ok := cur[p].(obj)
		if !ok {
			return obj{}
		}
		cur = next
	}
	return cur
}

func str(m obj, path ...string) string {
	s, _ := get(m, path[:len(path)-1]...)[path[len(path)-1]].(string)
	return s
}

func strs(v any) []string {
	var out []string
	switch t := v.(type) {
	case []any:
		for _, x := range t {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	case []string:
		out = t
	}
	return out
}


func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
