// Package stdopenapi works with OpenAPI documents.
package stdopenapi

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// DownConvertVersion is the OpenAPI version that DownConvert produces.
const DownConvertVersion = "3.0.3"

// operationKeys are the keys of a path item that hold an operation.
var operationKeys = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}

// unsupportedSchemaKeywords are the schema keywords of OpenAPI 3.1, from JSON
// Schema 2020-12, that OpenAPI 3.0 has no equivalent for.
var unsupportedSchemaKeywords = []string{
	"$anchor", "$comment", "$defs", "$dynamicAnchor", "$dynamicRef", "$id", "$schema",
	"contains", "contentEncoding", "contentMediaType", "contentSchema",
	"dependentRequired", "dependentSchemas", "else", "if", "maxContains", "minContains",
	"patternProperties", "prefixItems", "propertyNames", "then",
	"unevaluatedItems",
}

// DownConvert converts an OpenAPI 3.1 document, in YAML or JSON, to an OpenAPI
// 3.0.3 document in YAML, for clients that do not support 3.1. It rewrites the
// 3.1 constructs that have a 3.0 equivalent:
//
//   - a type list with "null", such as [string, "null"], becomes the type and
//     nullable: true;
//   - a schema's examples become its first example;
//   - const becomes an enum of one value;
//   - a numeric exclusiveMinimum or exclusiveMaximum becomes minimum or
//     maximum, with the boolean exclusiveMinimum or exclusiveMaximum;
//   - a $ref with sibling keywords becomes an allOf of the $ref, next to the
//     siblings, since OpenAPI 3.0 ignores the siblings of a $ref;
//   - a boolean unevaluatedProperties is dropped: OpenAPI 3.0 cannot close an
//     object over the properties of its allOf, anyOf, or oneOf, so the object
//     accepts other properties too. protoc-gen-connect-openapi writes
//     unevaluatedProperties: false for messages with a oneof.
//
// It fails on the 3.1 constructs that OpenAPI 3.0 cannot express, such as
// webhooks, prefixItems, or a type list of several types, and reports where
// each one is, instead of dropping them. It keeps the order of keys, and writes
// mappings and sequences in block style.
//
// It follows the rules of Apiture's openapi-down-convert
// (https://github.com/apiture/openapi-down-convert) where both convert a
// construct, but fails where that tool drops one. For more parity, it could
// also drop the constructs that only document: the summary and description
// next to a $ref, info.summary, info.license.identifier, $id and $schema, and
// $comment, which that tool renames to x-comment. And it could convert
// contentEncoding: base64 to format: byte, and contentMediaType to format:
// binary.
func DownConvert(spec []byte) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(spec, &doc); err != nil {
		return nil, fmt.Errorf("parse document: %w", err)
	}

	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("document is not a mapping")
	}

	var conv converter
	conv.document(doc.Content[0])

	if err := errors.Join(conv.errs...); err != nil {
		return nil, err
	}

	block(&doc)

	var buf bytes.Buffer

	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)

	if err := enc.Encode(&doc); err != nil {
		return nil, fmt.Errorf("encode document: %w", err)
	}

	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("encode document: %w", err)
	}

	return buf.Bytes(), nil
}

// converter rewrites a document in place, and collects what it cannot
// rewrite.
type converter struct {
	errs []error
}

// failf records that the node at path cannot be converted.
func (c *converter) failf(path []string, format string, args ...any) {
	c.errs = append(c.errs, fmt.Errorf("%s: %s", pointer(path), fmt.Sprintf(format, args...)))
}

// unsupported fails each of keys that m has.
func (c *converter) unsupported(path []string, m *yaml.Node, keys ...string) {
	for _, key := range keys {
		if get(m, key) != nil {
			c.failf(at(path, key), "has no OpenAPI 3.0 equivalent")
		}
	}
}

func (c *converter) document(root *yaml.Node) {
	version := get(root, "openapi")
	if version == nil || !strings.HasPrefix(version.Value, "3.1.") {
		c.failf([]string{"openapi"}, "want an OpenAPI 3.1 document")

		return
	}

	version.Value = DownConvertVersion
	version.Tag = "!!str"
	version.Style = 0

	c.unsupported(nil, root, "webhooks", "jsonSchemaDialect")

	if info := mapping(get(root, "info")); info != nil {
		c.unsupported([]string{"info"}, info, "summary")

		if license := mapping(get(info, "license")); license != nil {
			c.unsupported([]string{"info", "license"}, license, "identifier")
		}
	}

	each([]string{"paths"}, get(root, "paths"), c.pathItem)

	if components := mapping(get(root, "components")); components != nil {
		path := []string{"components"}
		c.unsupported(path, components, "pathItems")
		each(at(path, "schemas"), get(components, "schemas"), c.schema)
		each(at(path, "parameters"), get(components, "parameters"), c.parameter)
		each(at(path, "headers"), get(components, "headers"), c.parameter)
		each(at(path, "requestBodies"), get(components, "requestBodies"), c.withContent)
		each(at(path, "responses"), get(components, "responses"), c.response)
		each(at(path, "callbacks"), get(components, "callbacks"), c.callback)
	}
}

func (c *converter) pathItem(path []string, item *yaml.Node) {
	if c.reference(path, item) {
		return
	}

	eachItem(at(path, "parameters"), get(item, "parameters"), c.parameter)

	for _, key := range operationKeys {
		if op := mapping(get(item, key)); op != nil {
			c.operation(at(path, key), op)
		}
	}
}

func (c *converter) operation(path []string, op *yaml.Node) {
	eachItem(at(path, "parameters"), get(op, "parameters"), c.parameter)

	if body := get(op, "requestBody"); body != nil {
		c.withContent(at(path, "requestBody"), body)
	}

	each(at(path, "responses"), get(op, "responses"), c.response)
	each(at(path, "callbacks"), get(op, "callbacks"), c.callback)
}

func (c *converter) callback(path []string, callback *yaml.Node) {
	if c.reference(path, callback) {
		return
	}

	each(path, callback, c.pathItem)
}

// parameter converts a parameter or a header, which have the same shape.
func (c *converter) parameter(path []string, param *yaml.Node) {
	if c.reference(path, param) {
		return
	}

	if schema := get(param, "schema"); schema != nil {
		c.schema(at(path, "schema"), schema)
	}

	each(at(path, "content"), get(param, "content"), c.mediaType)
}

func (c *converter) response(path []string, response *yaml.Node) {
	if c.reference(path, response) {
		return
	}

	each(at(path, "headers"), get(response, "headers"), c.parameter)
	each(at(path, "content"), get(response, "content"), c.mediaType)
}

// withContent converts a request body, or another object whose content is
// its only part with schemas.
func (c *converter) withContent(path []string, n *yaml.Node) {
	if c.reference(path, n) {
		return
	}

	each(at(path, "content"), get(n, "content"), c.mediaType)
}

func (c *converter) mediaType(path []string, media *yaml.Node) {
	if schema := get(media, "schema"); schema != nil {
		c.schema(at(path, "schema"), schema)
	}
}

// reference reports whether n is a reference object, and fails the summary
// and description that OpenAPI 3.1 allows next to its $ref.
func (c *converter) reference(path []string, n *yaml.Node) bool {
	if n.Kind != yaml.MappingNode || get(n, "$ref") == nil {
		return false
	}

	c.unsupported(path, n, "summary", "description")

	return true
}

func (c *converter) schema(path []string, schema *yaml.Node) {
	if schema.Kind != yaml.MappingNode {
		c.failf(path, "a schema that is not an object has no OpenAPI 3.0 equivalent")

		return
	}

	c.unsupported(path, schema, unsupportedSchemaKeywords...)
	c.typeList(path, schema)
	c.constant(path, schema)
	c.examples(path, schema)
	c.exclusiveBound(path, schema, "exclusiveMinimum", "minimum")
	c.exclusiveBound(path, schema, "exclusiveMaximum", "maximum")
	c.unevaluatedProperties(path, schema)

	each(at(path, "properties"), get(schema, "properties"), c.schema)

	for _, key := range []string{"items", "additionalProperties", "not"} {
		// additionalProperties may also be a boolean, which 3.0 allows.
		if sub := get(schema, key); sub != nil && (key != "additionalProperties" || sub.Kind == yaml.MappingNode) {
			c.schema(at(path, key), sub)
		}
	}

	for _, key := range []string{"allOf", "anyOf", "oneOf"} {
		eachItem(at(path, key), get(schema, key), c.schema)
	}

	c.referenceWithSiblings(schema)
}

// typeList rewrites a type list of one type and "null" to the type and
// nullable: true.
func (c *converter) typeList(path []string, schema *yaml.Node) {
	typ := get(schema, "type")
	if typ == nil {
		return
	}

	if typ.Kind == yaml.ScalarNode {
		if typ.Value == "null" {
			c.failf(at(path, "type"), "the null type has no OpenAPI 3.0 equivalent")
		}

		return
	}

	var types []*yaml.Node

	nullable := false

	for _, t := range typ.Content {
		if t.Value == "null" {
			nullable = true

			continue
		}

		types = append(types, t)
	}

	if len(types) != 1 {
		c.failf(at(path, "type"), "a type list of %d types has no OpenAPI 3.0 equivalent", len(types))

		return
	}

	set(schema, "type", types[0])

	if nullable {
		set(schema, "nullable", boolean(true))
	}
}

// constant rewrites const to an enum of one value.
func (c *converter) constant(path []string, schema *yaml.Node) {
	value := get(schema, "const")
	if value == nil {
		return
	}

	if get(schema, "enum") != nil {
		c.failf(at(path, "const"), "const next to enum has no OpenAPI 3.0 equivalent")

		return
	}

	rename(schema, "const", "enum", &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{value}})
}

// examples rewrites a schema's examples to its first example.
func (c *converter) examples(path []string, schema *yaml.Node) {
	examples := get(schema, "examples")
	if examples == nil {
		return
	}

	if examples.Kind != yaml.SequenceNode {
		c.failf(at(path, "examples"), "want a list of examples")

		return
	}

	if len(examples.Content) == 0 || get(schema, "example") != nil {
		del(schema, "examples")

		return
	}

	rename(schema, "examples", "example", examples.Content[0])
}

// exclusiveBound rewrites a numeric exclusive bound, such as exclusiveMinimum:
// 5, to the bound and the boolean, such as minimum: 5 and exclusiveMinimum:
// true.
func (c *converter) exclusiveBound(path []string, schema *yaml.Node, exclusive, bound string) {
	value := get(schema, exclusive)
	if value == nil || value.Tag == "!!bool" {
		return
	}

	if get(schema, bound) != nil {
		c.failf(at(path, exclusive), "%s next to %s has no OpenAPI 3.0 equivalent", exclusive, bound)

		return
	}

	rename(schema, exclusive, bound, value)
	set(schema, exclusive, boolean(true))
}

// unevaluatedProperties drops a boolean unevaluatedProperties, which OpenAPI
// 3.0 has no equivalent for, and fails a schema-valued one, which would change
// what the object accepts.
func (c *converter) unevaluatedProperties(path []string, schema *yaml.Node) {
	value := get(schema, "unevaluatedProperties")
	if value == nil {
		return
	}

	if value.Tag != "!!bool" {
		c.failf(at(path, "unevaluatedProperties"), "a schema-valued unevaluatedProperties has no OpenAPI 3.0 equivalent")

		return
	}

	del(schema, "unevaluatedProperties")
}

// referenceWithSiblings moves the $ref of a schema with other keywords into
// its allOf, because OpenAPI 3.0 ignores the siblings of a $ref.
func (c *converter) referenceWithSiblings(schema *yaml.Node) {
	ref := get(schema, "$ref")
	if ref == nil || len(schema.Content) == 2 {
		return
	}

	del(schema, "$ref")

	refSchema := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{scalar("$ref"), ref}}

	if allOf := get(schema, "allOf"); allOf != nil && allOf.Kind == yaml.SequenceNode {
		allOf.Content = slices.Insert(allOf.Content, 0, refSchema)

		return
	}

	set(schema, "allOf", &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{refSchema}})
}

// block writes every mapping and sequence under n in block style, and every
// key without quotes unless it needs them, so that a document in JSON, or in
// YAML with flow style, comes out as block YAML.
func block(n *yaml.Node) {
	n.Style &^= yaml.FlowStyle

	if n.Kind == yaml.MappingNode {
		for i := 0; i < len(n.Content); i += 2 {
			n.Content[i].Style &^= yaml.DoubleQuotedStyle | yaml.SingleQuotedStyle
		}
	}

	for _, child := range n.Content {
		block(child)
	}
}

// each calls fn with each value of mapping m, if m is a mapping.
func each(path []string, m *yaml.Node, fn func(path []string, n *yaml.Node)) {
	if mapping(m) == nil {
		return
	}

	for i := 0; i+1 < len(m.Content); i += 2 {
		fn(at(path, m.Content[i].Value), m.Content[i+1])
	}
}

// eachItem calls fn with each item of sequence s, if s is a sequence.
func eachItem(path []string, s *yaml.Node, fn func(path []string, n *yaml.Node)) {
	if s == nil || s.Kind != yaml.SequenceNode {
		return
	}

	for i, item := range s.Content {
		fn(at(path, fmt.Sprint(i)), item)
	}
}

// mapping returns n if it is a mapping, and nil otherwise.
func mapping(n *yaml.Node) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}

	return n
}

// get returns the value of key in mapping m, or nil.
func get(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}

	return nil
}

// set sets key in mapping m to value, in place if m has key, and last
// otherwise.
func set(m *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = value

			return
		}
	}

	m.Content = append(m.Content, scalar(key), value)
}

// rename replaces key in mapping m with newKey and value, in its place.
func rename(m *yaml.Node, key, newKey string, value *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i] = scalar(newKey)
			m.Content[i+1] = value

			return
		}
	}
}

// del removes key from mapping m.
func del(m *yaml.Node, key string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = slices.Delete(m.Content, i, i+2)

			return
		}
	}
}

func scalar(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

func boolean(value bool) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: fmt.Sprint(value)}
}

// at returns path with key appended, without changing path.
func at(path []string, keys ...string) []string {
	return append(slices.Clip(path), keys...)
}

// pointer formats path as a JSON pointer fragment, such as
// #/paths/~1v1~1things/get.
func pointer(path []string) string {
	escaped := make([]string, len(path))
	for i, key := range path {
		escaped[i] = strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
	}

	return "#/" + strings.Join(escaped, "/")
}
