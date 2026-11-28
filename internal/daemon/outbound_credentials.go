package daemon

import (
	"encoding/json"
	"errors"
	"strings"

	"go.yaml.in/yaml/v3"
)

// outboundTokenFiles reads the security-sensitive map before Viper's
// case-insensitive key normalization can collapse distinct origin keys.
// The nonempty JSON environment override replaces the entire file map.
func outboundTokenFiles(raw []byte, env string) (map[string]string, error) {
	var files map[string]string
	if len(raw) != 0 {
		var root map[string]yaml.Node
		if err := yaml.Unmarshal(raw, &root); err != nil {
			return nil, errors.New("invalid configuration mapping")
		}
		for key := range root {
			if strings.EqualFold(key, "delivery.token_files") {
				return nil, errors.New("delivery.token_files must be nested under delivery")
			}
		}
		delivery, err := credentialConfigField(root, "delivery")
		if err != nil {
			return nil, err
		}
		if delivery != nil {
			var fields map[string]yaml.Node
			if err := delivery.Decode(&fields); err != nil {
				return nil, errors.New("invalid delivery configuration mapping")
			}
			node, err := credentialConfigField(fields, "token_files")
			if err != nil {
				return nil, err
			}
			if node != nil {
				files, err = decodeOutboundTokenFiles(node)
				if err != nil {
					return nil, err
				}
			}
		}
	}
	if env != "" {
		// JSON is a YAML subset. The YAML decoder also rejects duplicate
		// keys, unlike encoding/json's default map decoding.
		var root yaml.Node
		if !json.Valid([]byte(env)) || yaml.Unmarshal([]byte(env), &root) != nil || len(root.Content) != 1 {
			return nil, errors.New("AGENTIPLEX_DELIVERY_TOKEN_FILES must be a JSON object of origins to credential files")
		}
		var err error
		files, err = decodeOutboundTokenFiles(root.Content[0])
		if err != nil {
			return nil, errors.New("AGENTIPLEX_DELIVERY_TOKEN_FILES must be a JSON object of origins to credential files with unique keys")
		}
	}
	return files, nil
}

func credentialConfigField(fields map[string]yaml.Node, name string) (*yaml.Node, error) {
	var found *yaml.Node
	for key, node := range fields {
		if strings.EqualFold(key, name) {
			if found != nil {
				return nil, errors.New("ambiguous configuration field: " + name)
			}
			found = &node
		}
	}
	return found, nil
}

// Inspect the explicit mapping rather than decoding into a Go map, which
// could coerce non-string values or silently apply YAML merge overrides.
func decodeOutboundTokenFiles(node *yaml.Node) (map[string]string, error) {
	if node.Kind != yaml.MappingNode {
		return nil, errors.New("delivery.token_files must be an explicit mapping of origins to credential files")
	}
	files := make(map[string]string, len(node.Content)/2)
	for i := 0; i < len(node.Content); i += 2 {
		key, value := node.Content[i], node.Content[i+1]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || value.Kind != yaml.ScalarNode || value.Tag != "!!str" {
			return nil, errors.New("delivery.token_files requires string origin keys and credential file paths")
		}
		if _, exists := files[key.Value]; exists {
			return nil, errors.New("delivery.token_files contains a duplicate origin key")
		}
		files[key.Value] = value.Value
	}
	return files, nil
}
