package stdopenapi_test

import (
	"os"
	"strings"
	"testing"

	"github.com/advdv/stdgo/stdopenapi"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

// document returns an OpenAPI 3.1 document with schema as component S.
func document(schema string) string {
	return "openapi: 3.1.0\ninfo: {title: t, version: v}\npaths: {}\ncomponents:\n  schemas:\n    S: " + schema + "\n"
}

// convertedSchema converts document(schema), and returns component S.
func convertedSchema(t *testing.T, schema string) any {
	t.Helper()

	out, err := stdopenapi.DownConvert([]byte(document(schema)))
	require.NoError(t, err)

	var doc struct {
		OpenAPI    string `yaml:"openapi"`
		Components struct {
			Schemas map[string]any `yaml:"schemas"`
		} `yaml:"components"`
	}
	require.NoError(t, yaml.Unmarshal(out, &doc))
	require.Equal(t, stdopenapi.DownConvertVersion, doc.OpenAPI)

	return doc.Components.Schemas["S"]
}

func parse(t *testing.T, s string) any {
	t.Helper()

	var v any
	require.NoError(t, yaml.Unmarshal([]byte(s), &v))

	return v
}

func TestDownConvertRewritesSchemas(t *testing.T) {
	for _, tc := range []struct {
		desc   string
		schema string
		want   string
	}{
		{"type with null", `{type: [string, "null"]}`, `{type: string, nullable: true}`},
		{"type list of one", `{type: [integer]}`, `{type: integer}`},
		{"examples", `{type: string, examples: [a, b]}`, `{type: string, example: a}`},
		{"empty examples", `{type: string, examples: []}`, `{type: string}`},
		{"examples next to example", `{example: a, examples: [b]}`, `{example: a}`},
		{"const", `{const: ""}`, `{enum: [""]}`},
		{"exclusiveMinimum", `{exclusiveMinimum: 0}`, `{minimum: 0, exclusiveMinimum: true}`},
		{"exclusiveMaximum", `{exclusiveMaximum: 10}`, `{maximum: 10, exclusiveMaximum: true}`},
		{"boolean exclusiveMinimum", `{minimum: 0, exclusiveMinimum: true}`, `{minimum: 0, exclusiveMinimum: true}`},
		{"$ref with siblings", `{$ref: '#/components/schemas/T', description: d}`, `{description: d, allOf: [{$ref: '#/components/schemas/T'}]}`},
		{"$ref with siblings and allOf", `{$ref: '#/a', allOf: [{$ref: '#/b'}], title: t}`, `{allOf: [{$ref: '#/a'}, {$ref: '#/b'}], title: t}`},
		{"$ref alone", `{$ref: '#/components/schemas/T'}`, `{$ref: '#/components/schemas/T'}`},
		{"property", `{properties: {p: {type: [string, "null"]}}}`, `{properties: {p: {type: string, nullable: true}}}`},
		{"property named after a keyword", `{properties: {const: {type: string}}}`, `{properties: {const: {type: string}}}`},
		{"items", `{type: array, items: {const: 1}}`, `{type: array, items: {enum: [1]}}`},
		{"additionalProperties", `{additionalProperties: {examples: [1]}}`, `{additionalProperties: {example: 1}}`},
		{"boolean additionalProperties", `{additionalProperties: false}`, `{additionalProperties: false}`},
		{"oneOf", `{oneOf: [{const: a}, {const: b}]}`, `{oneOf: [{enum: [a]}, {enum: [b]}]}`},
		{"not", `{not: {const: a}}`, `{not: {enum: [a]}}`},
		{"unevaluatedProperties false", `{oneOf: [{properties: {a: {type: string}}}], unevaluatedProperties: false}`, `{oneOf: [{properties: {a: {type: string}}}]}`},
		{"unevaluatedProperties true", `{unevaluatedProperties: true}`, `{}`},
		// The cases below come from the tests of Apiture's openapi-down-convert
		// (https://github.com/apiture/openapi-down-convert, ISC license).
		{
			"property description next to $ref (Apiture issue #37)",
			`{type: object, properties: {x: {description: x (property), $ref: '#/components/schemas/x'}}}`,
			`{type: object, properties: {x: {description: x (property), allOf: [{$ref: '#/components/schemas/x'}]}}}`,
		},
		{
			"$ref with examples and siblings",
			`{description: d, title: t, $ref: '#/components/schemas/a', examples: [Foo, Bar]}`,
			`{description: d, title: t, example: Foo, allOf: [{$ref: '#/components/schemas/a'}]}`,
		},
		{
			"const in the properties of oneOf branches",
			`{oneOf: [{type: object, properties: {type: {const: s}, value: {type: string}}}, {type: object, properties: {type: {const: n}, value: {type: number}}}]}`,
			`{oneOf: [{type: object, properties: {type: {enum: [s]}, value: {type: string}}}, {type: object, properties: {type: {enum: [n]}, value: {type: number}}}]}`,
		},
		{
			"examples that are lists",
			`{type: array, items: {type: string}, examples: [[Presentation], [Presentation, Notifications]]}`,
			`{type: array, items: {type: string}, example: [Presentation]}`,
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			require.Equal(t, parse(t, tc.want), convertedSchema(t, tc.schema))
		})
	}
}

func TestDownConvertRewritesSchemasInPathsAndComponents(t *testing.T) {
	in := `openapi: 3.1.0
info: {title: t, version: v}
paths:
  /things/{id}:
    parameters:
      - {name: id, in: path, required: true, schema: {examples: [a]}}
    get:
      parameters:
        - {name: q, in: query, schema: {const: x}}
      requestBody:
        content: {application/json: {schema: {examples: [b]}}}
      responses:
        "200":
          headers: {X-H: {schema: {examples: [c]}}}
          content: {application/json: {schema: {examples: [d]}}}
      callbacks:
        cb: {"{$url}": {post: {responses: {"200": {content: {application/json: {schema: {examples: [e]}}}}}}}}
components:
  parameters: {P: {name: p, in: query, content: {application/json: {schema: {examples: [f]}}}}}
  headers: {H: {schema: {examples: [g]}}}
  requestBodies: {B: {content: {application/json: {schema: {examples: [h]}}}}}
  responses: {R: {content: {application/json: {schema: {examples: [i]}}}}}
`
	out, err := stdopenapi.DownConvert([]byte(in))
	require.NoError(t, err)

	doc := parse(t, string(out))
	for want, path := range map[string][]any{
		"a": {"paths", "/things/{id}", "parameters", 0, "schema"},
		"b": {"paths", "/things/{id}", "get", "requestBody", "content", "application/json", "schema"},
		"c": {"paths", "/things/{id}", "get", "responses", "200", "headers", "X-H", "schema"},
		"d": {"paths", "/things/{id}", "get", "responses", "200", "content", "application/json", "schema"},
		"e": {
			"paths", "/things/{id}", "get", "callbacks", "cb", "{$url}", "post",
			"responses", "200", "content", "application/json", "schema",
		},
		"f": {"components", "parameters", "P", "content", "application/json", "schema"},
		"g": {"components", "headers", "H", "schema"},
		"h": {"components", "requestBodies", "B", "content", "application/json", "schema"},
		"i": {"components", "responses", "R", "content", "application/json", "schema"},
	} {
		require.Equal(t, map[string]any{"example": want}, lookup(t, doc, path...), "at %v", path)
	}

	require.Equal(t, map[string]any{"enum": []any{"x"}},
		lookup(t, doc, "paths", "/things/{id}", "get", "parameters", 0, "schema"))
}

// lookup returns the value at path in v, a parsed document, where each key
// of path is a mapping key or a sequence index.
func lookup(t *testing.T, v any, path ...any) any {
	t.Helper()

	for _, key := range path {
		switch k := key.(type) {
		case string:
			m, ok := v.(map[string]any)
			require.True(t, ok, "not a mapping at %q", k)
			v = m[k]
		case int:
			s, ok := v.([]any)
			require.True(t, ok, "not a sequence at %d", k)
			v = s[k]
		}
	}

	return v
}

func TestDownConvertKeepsMediaTypeExamples(t *testing.T) {
	in := `openapi: 3.1.0
info: {title: t, version: v}
paths:
  /things:
    get:
      responses:
        "200":
          content:
            application/json:
              examples: {one: {value: 1}}
`
	out, err := stdopenapi.DownConvert([]byte(in))
	require.NoError(t, err)
	require.Equal(t, map[string]any{"one": map[string]any{"value": 1}},
		lookup(t, parse(t, string(out)), "paths", "/things", "get", "responses", "200", "content", "application/json", "examples"))
}

func TestDownConvertRejectsUnsupported(t *testing.T) {
	for _, tc := range []struct {
		desc string
		doc  string
		want string
	}{
		{"OpenAPI 3.0", "openapi: 3.0.3\npaths: {}\n", "#/openapi: want an OpenAPI 3.1 document"},
		{"no version", "paths: {}\n", "#/openapi: want an OpenAPI 3.1 document"},
		{"not a mapping", "- a\n", "document is not a mapping"},
		{"webhooks", "openapi: 3.1.0\nwebhooks: {}\n", "#/webhooks: has no OpenAPI 3.0 equivalent"},
		{"jsonSchemaDialect", "openapi: 3.1.0\njsonSchemaDialect: x\n", "#/jsonSchemaDialect: has no OpenAPI 3.0 equivalent"},
		{"info summary", "openapi: 3.1.0\ninfo: {summary: s}\n", "#/info/summary: has no OpenAPI 3.0 equivalent"},
		{"license identifier", "openapi: 3.1.0\ninfo: {license: {identifier: MIT}}\n", "#/info/license/identifier: has no OpenAPI 3.0 equivalent"},
		{"pathItems", "openapi: 3.1.0\ncomponents: {pathItems: {}}\n", "#/components/pathItems: has no OpenAPI 3.0 equivalent"},
		{"prefixItems", document(`{prefixItems: [{type: string}]}`), "#/components/schemas/S/prefixItems: has no OpenAPI 3.0 equivalent"},
		{"$defs", document(`{$defs: {}}`), "#/components/schemas/S/$defs: has no OpenAPI 3.0 equivalent"},
		{"if", document(`{if: {}}`), "#/components/schemas/S/if: has no OpenAPI 3.0 equivalent"},
		{"type list of two", document(`{type: [string, integer]}`), "#/components/schemas/S/type: a type list of 2 types has no OpenAPI 3.0 equivalent"},
		{"null type", document(`{type: "null"}`), "#/components/schemas/S/type: the null type has no OpenAPI 3.0 equivalent"},
		{"const next to enum", document(`{const: a, enum: [a]}`), "#/components/schemas/S/const: const next to enum has no OpenAPI 3.0 equivalent"},
		{"schema-valued unevaluatedProperties", document(`{unevaluatedProperties: {type: string}}`), "#/components/schemas/S/unevaluatedProperties: a schema-valued unevaluatedProperties has no OpenAPI 3.0 equivalent"},
		{"exclusiveMinimum next to minimum", document(`{exclusiveMinimum: 1, minimum: 0}`), "#/components/schemas/S/exclusiveMinimum: exclusiveMinimum next to minimum has no OpenAPI 3.0 equivalent"},
		{"boolean schema", document(`true`), "#/components/schemas/S: a schema that is not an object has no OpenAPI 3.0 equivalent"},
		{"reference with a summary", "openapi: 3.1.0\npaths: {/a: {$ref: '#/x', summary: s}}\n", "#/paths/~1a/summary: has no OpenAPI 3.0 equivalent"},
		{"nested in a path", "openapi: 3.1.0\npaths: {/a/b: {get: {parameters: [{name: q, in: query, schema: {if: {}}}]}}}\n", "#/paths/~1a~1b/get/parameters/0/schema/if: has no OpenAPI 3.0 equivalent"},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			_, err := stdopenapi.DownConvert([]byte(tc.doc))
			require.EqualError(t, err, tc.want)
		})
	}
}

func TestDownConvertReportsEveryUnsupportedConstruct(t *testing.T) {
	_, err := stdopenapi.DownConvert([]byte(document(`{properties: {a: {if: {}}, b: {prefixItems: []}}}`)))
	require.Error(t, err)
	require.Equal(t, []string{
		"#/components/schemas/S/properties/a/if: has no OpenAPI 3.0 equivalent",
		"#/components/schemas/S/properties/b/prefixItems: has no OpenAPI 3.0 equivalent",
	}, strings.Split(err.Error(), "\n"))
}

func TestDownConvertAcceptsJSON(t *testing.T) {
	out, err := stdopenapi.DownConvert([]byte(`{"openapi": "3.1.0", "paths": {}}`))
	require.NoError(t, err)
	require.Equal(t, "openapi: 3.0.3\npaths: {}\n", string(out), "written as block YAML")
}

// TestDownConvertGolden converts testdata/in-3.1.yaml, a document as
// protoc-gen-connect-openapi generates it, and requires
// testdata/want-3.0.yaml, which shows the layout and key order of the result.
func TestDownConvertGolden(t *testing.T) {
	in, err := os.ReadFile("testdata/in-3.1.yaml")
	require.NoError(t, err)

	want, err := os.ReadFile("testdata/want-3.0.yaml")
	require.NoError(t, err)

	out, err := stdopenapi.DownConvert(in)
	require.NoError(t, err)
	require.Equal(t, string(want), string(out))
}
