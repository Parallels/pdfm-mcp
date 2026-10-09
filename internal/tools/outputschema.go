package tools

import (
	"github.com/google/jsonschema-go/jsonschema"
)

// The jsonschema-go inference library emits `["null", "array"]` for every Go
// slice, because a slice can be nil. That is legal JSON Schema, but less
// portable: clients that map tool schemas onto a single-type dialect (e.g.
// the OpenAPI subset used for Gemini function declarations) may reject the
// tool or drop the constraint. Our handlers guarantee the affected fields
// are never nil (VM.Normalize + the handlers themselves), so we register the
// tools with a normalized schema where those fields are plain "array" types.

// normalizeNullArrays rewrites multi-type unions like ["null","array"] into a
// single "type" on every node of the schema tree, in place.
func normalizeNullArrays(s *jsonschema.Schema) {
	if s == nil {
		return
	}
	if hasNullAndArray(s.Types) {
		s.Types = nil
		s.Type = "array"
	}
	for _, p := range s.Properties {
		normalizeNullArrays(p)
	}
	for _, p := range s.PatternProperties {
		normalizeNullArrays(p)
	}
	normalizeNullArrays(s.AdditionalProperties)
	if s.Items != nil {
		normalizeNullArrays(s.Items)
	}
	for _, it := range s.ItemsArray {
		normalizeNullArrays(it)
	}
	for _, it := range s.PrefixItems {
		normalizeNullArrays(it)
	}
	for _, br := range s.AllOf {
		normalizeNullArrays(br)
	}
	for _, br := range s.AnyOf {
		normalizeNullArrays(br)
	}
	for _, br := range s.OneOf {
		normalizeNullArrays(br)
	}
	normalizeNullArrays(s.Not)
	normalizeNullArrays(s.UnevaluatedProperties)
	normalizeNullArrays(s.UnevaluatedItems)
}

func hasNullAndArray(types []string) bool {
	hasNull, hasArray := false, false
	for _, t := range types {
		switch t {
		case "null":
			hasNull = true
		case "array":
			hasArray = true
		}
	}
	return hasNull && hasArray
}

// outputSchemaFor builds the portable output schema for the given output
// type: inferred with the same library the SDK uses, then normalized so no
// node carries a ["null","array"] union.
func outputSchemaFor[T any](name string) *jsonschema.Schema {
	schema, err := jsonschema.For[T](&jsonschema.ForOptions{})
	if err != nil {
		// Only reachable at registration time (server startup) with a
		// compile-time-known type; failing fast is the right behavior.
		panic("tools: output schema for " + name + ": " + err.Error())
	}
	normalizeNullArrays(schema)
	return schema
}
