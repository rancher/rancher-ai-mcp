package utils

import "github.com/google/jsonschema-go/jsonschema"

// ForceArrayTypes replaces the ["null", "array"] type that jsonschema-go infers for slices
// with a single "array" type, recursively, so agent clients (like Gemini / Vertex AI) can
// validate function declarations.
func ForceArrayTypes(s *jsonschema.Schema) {
	if s == nil {
		return
	}
	if s.Items != nil {
		s.Type = "array"
		s.Types = nil
		ForceArrayTypes(s.Items)
	}
	for _, prop := range s.Properties {
		ForceArrayTypes(prop)
	}
}
