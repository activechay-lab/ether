package studio

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// schemaType follows the JSON contract, including anonymous presentation structs.
// Keeping this in the test makes new Go fields fail the published-schema gate.
func schemaType(typ reflect.Type) map[string]any {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	switch typ.Kind() {
	case reflect.Struct:
		props := map[string]any{}
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			name := strings.Split(f.Tag.Get("json"), ",")[0]
			if name == "" || name == "-" {
				continue
			}
			props[name] = schemaType(f.Type)
		}
		if typ == reflect.TypeFor[plugins.KindField]() {
			props["type"] = map[string]any{"enum": []string{"string", "int", "float", "bool", "date", "duration", "enum", "strings", "people"}}
		}
		return map[string]any{"type": "object", "properties": props}
	case reflect.Slice, reflect.Array:
		return map[string]any{"type": "array", "items": schemaType(typ.Elem())}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": schemaType(typ.Elem())}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	default:
		return map[string]any{"type": "integer"}
	}
}

func TestPublishedManifestSchema(t *testing.T) {
	const path = "../../../schemas/plugin-manifest.v1.json"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err = json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	ether := doc["properties"].(map[string]any)["ether"].(map[string]any)["properties"].(map[string]any)
	// Extract every typed kind/capability constant from the authoritative contract.
	file, err := parser.ParseFile(token.NewFileSet(), "../contract.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	enums := map[string][]string{}
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		typ, ok := spec.Type.(*ast.Ident)
		if !ok || (typ.Name != "Kind" && typ.Name != "Capability") {
			return true
		}
		for _, v := range spec.Values {
			if lit, ok := v.(*ast.BasicLit); ok {
				value, e := strconv.Unquote(lit.Value)
				if e == nil {
					enums[typ.Name] = append(enums[typ.Name], value)
				}
			}
		}
		return true
	})
	expected := map[string]any{}
	for key, value := range ether {
		expected[key] = value
	}
	expected["kind"] = map[string]any{"enum": enums["Kind"]}
	expected["capabilities"] = map[string]any{"type": "array", "uniqueItems": true, "items": map[string]any{"enum": enums["Capability"]}}
	contrib := ether["contributes"].(map[string]any)["properties"].(map[string]any)
	kind := schemaType(reflect.TypeFor[plugins.MediaKindMeta]())
	adapter := schemaType(reflect.TypeFor[plugins.FrontendAdapterMeta]())
	wantKinds := map[string]any{"type": "array", "items": kind}
	wantAdapters := map[string]any{"type": "array", "items": adapter}
	equal := func(a, b any) bool { x, _ := json.Marshal(a); y, _ := json.Marshal(b); return string(x) == string(y) }
	changed := !equal(ether["kind"], expected["kind"]) || !equal(ether["capabilities"], expected["capabilities"]) || !equal(contrib["mediaKinds"], wantKinds) || !equal(contrib["frontendAdapters"], wantAdapters)
	if changed {
		if os.Getenv("UPDATE_MANIFEST_SCHEMA") != "1" {
			t.Fatal("manifest schema drift: run UPDATE_MANIFEST_SCHEMA=1 go test ./internal/plugins/studio -run TestPublishedManifestSchema")
		}
		ether["kind"] = expected["kind"]
		ether["capabilities"] = expected["capabilities"]
		contrib["mediaKinds"] = wantKinds
		contrib["frontendAdapters"] = wantAdapters
		raw, err = json.MarshalIndent(doc, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, append(raw, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
	}
	normalized, _ := json.Marshal(doc)
	json.Unmarshal(normalized, &doc)
	compiler := jsonschema.NewCompiler()
	if err = compiler.AddResource("manifest.json", doc); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, tpl := range Templates() {
		t.Run(tpl.ID, func(t *testing.T) {
			var manifest any
			if err := json.Unmarshal([]byte(tpl.Files["manifest.json"]), &manifest); err != nil {
				t.Fatal(err)
			}
			if err := schema.Validate(manifest); err != nil {
				t.Fatal(err)
			}
		})
	}
}
