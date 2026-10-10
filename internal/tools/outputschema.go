package tools

import (
	"slices"

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
// node carries a ["null","array"] union or any other multi-type "type"
// array, and no node carries no validation keyword at all.
func outputSchemaFor[T any](name string) *jsonschema.Schema {
	schema, err := jsonschema.For[T](&jsonschema.ForOptions{})
	if err != nil {
		// Only reachable at registration time (server startup) with a
		// compile-time-known type; failing fast is the right behavior.
		panic("tools: output schema for " + name + ": " + err.Error())
	}
	normalizeNullArrays(schema)
	splitNullableUnions(schema)
	pinBareNodes(schema)
	return schema
}

// pinBareNodes removes schema nodes that carry no validation keyword at
// all (the wire form of a bare {}): legal JSON Schema, but they constrain
// nothing, and some MCP clients reject them or drop the field. In this
// repo a bare node only arises from a Go any or map[string]any field, and
// the contract of every such field is a JSON object (prlctl's VM object, a
// step tool's structured output, a step tool's arguments). Therefore:
//   - a bare node standing for a value is pinned to {"type": "object"};
//   - a bare node standing for an object's additionalProperties (a
//     map[string]any's values may be anything) is deleted from the parent,
//     which is semantically identical: an object without the
//     additionalProperties keyword accepts any property value.
func pinBareNodes(s *jsonschema.Schema) {
	if s == nil {
		return
	}
	for _, p := range s.Properties {
		pinBareNodes(p)
	}
	for _, p := range s.PatternProperties {
		pinBareNodes(p)
	}
	if s.Items != nil {
		pinBareNodes(s.Items)
	}
	for _, it := range s.ItemsArray {
		pinBareNodes(it)
	}
	for _, it := range s.PrefixItems {
		pinBareNodes(it)
	}
	for _, br := range s.AllOf {
		pinBareNodes(br)
	}
	for _, br := range s.AnyOf {
		pinBareNodes(br)
	}
	for _, br := range s.OneOf {
		pinBareNodes(br)
	}
	pinBareNodes(s.Not)
	pinBareNodes(s.UnevaluatedProperties)
	pinBareNodes(s.UnevaluatedItems)
	pinBareNodes(s.ContentSchema)

	if s.AdditionalProperties != nil && isBare(s.AdditionalProperties) {
		s.AdditionalProperties = nil
	}
	if isBare(s) {
		s.Type = "object"
	}
}

// isBare reports whether s carries no validation keyword and no sub-schema
// (metadata such as description/title is not a validation keyword). On the
// wire such a node renders as a bare {} or as metadata only.
func isBare(s *jsonschema.Schema) bool {
	if s == nil {
		return true
	}
	return s.Type == "" &&
		len(s.Types) == 0 &&
		s.Enum == nil &&
		s.Const == nil &&
		s.MultipleOf == nil &&
		s.Minimum == nil &&
		s.Maximum == nil &&
		s.ExclusiveMinimum == nil &&
		s.ExclusiveMaximum == nil &&
		s.MinLength == nil &&
		s.MaxLength == nil &&
		s.Pattern == "" &&
		s.PrefixItems == nil &&
		s.Items == nil &&
		s.ItemsArray == nil &&
		s.MinItems == nil &&
		s.MaxItems == nil &&
		s.AdditionalItems == nil &&
		!s.UniqueItems &&
		s.Contains == nil &&
		s.MinContains == nil &&
		s.MaxContains == nil &&
		s.UnevaluatedItems == nil &&
		s.MinProperties == nil &&
		s.MaxProperties == nil &&
		len(s.Required) == 0 &&
		s.Properties == nil &&
		s.PatternProperties == nil &&
		s.AdditionalProperties == nil &&
		s.PropertyNames == nil &&
		s.UnevaluatedProperties == nil &&
		s.AllOf == nil &&
		s.AnyOf == nil &&
		s.OneOf == nil &&
		s.Not == nil &&
		s.If == nil &&
		s.Then == nil &&
		s.Else == nil &&
		s.ContentSchema == nil &&
		s.Format == "" &&
		s.Ref == "" &&
		s.Defs == nil &&
		s.Definitions == nil
}

// splitNullableUnions rewrites every multi-type "type" array that contains
// "null" (generated for Go pointer fields, e.g. "type":
// ["null","integer"]) into an anyOf branch per type, each branch carrying
// a single "type". Multi-type arrays are legal JSON Schema but less
// portable: clients that map tool schemas onto a single-type dialect (such
// as the OpenAPI subset used for Gemini function declarations) may reject
// the tool or drop the constraint. Unlike normalizeNullArrays this does
// NOT collapse the union to the non-null type: for pointer fields a JSON
// null is meaningful (e.g. delay_ms 0 versus the omitted default). The
// validation keywords travel with each branch; metadata (description)
// stays on the node.
func splitNullableUnions(s *jsonschema.Schema) {
	if s == nil {
		return
	}
	// Fix the children first so the clone below already carries them fixed.
	for _, p := range s.Properties {
		splitNullableUnions(p)
	}
	for _, p := range s.PatternProperties {
		splitNullableUnions(p)
	}
	splitNullableUnions(s.AdditionalProperties)
	if s.Items != nil {
		splitNullableUnions(s.Items)
	}
	for _, it := range s.ItemsArray {
		splitNullableUnions(it)
	}
	for _, it := range s.PrefixItems {
		splitNullableUnions(it)
	}
	for _, br := range s.AllOf {
		splitNullableUnions(br)
	}
	for _, br := range s.AnyOf {
		splitNullableUnions(br)
	}
	for _, br := range s.OneOf {
		splitNullableUnions(br)
	}
	splitNullableUnions(s.Not)
	splitNullableUnions(s.UnevaluatedProperties)
	splitNullableUnions(s.UnevaluatedItems)

	if len(s.Types) > 1 && slices.Contains(s.Types, "null") {
		base := s.CloneSchemas()
		base.Types = nil
		base.Description = "" // metadata stays on the anyOf node, not the branches
		base.Title = ""
		branches := make([]*jsonschema.Schema, 0, len(s.Types))
		for _, t := range s.Types {
			b := base.CloneSchemas()
			b.Type = t
			branches = append(branches, b)
		}
		s.Types = nil
		s.Type = ""
		s.AnyOf = branches
	}
}
