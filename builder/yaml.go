package builder

import "gopkg.in/yaml.v3"

// MarshalYAML serializes the OpenAPISpec to YAML bytes.
// yaml.v3 sorts map keys, so output is deterministic.
func MarshalYAML(spec *OpenAPISpec) ([]byte, error) {
	return yaml.Marshal(spec)
}
