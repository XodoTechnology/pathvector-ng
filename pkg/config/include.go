package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ExpandIncludes resolves `include:` directives in a YAML configuration
// document. The directive accepts a single filename or a list of filenames,
// resolved relative to baseDir. Included files may themselves contain
// includes. Merge semantics: mapping keys merge recursively with later
// sources winning; scalars and sequences are replaced wholesale. The
// including document's own keys take precedence over all includes, and
// includes listed later take precedence over earlier ones.
func ExpandIncludes(data []byte, baseDir string) ([]byte, error) {
	return expandIncludes(data, baseDir, nil)
}

func expandIncludes(data []byte, baseDir string, chain map[string]bool) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parsing YAML: %w", err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return data, nil
	}
	root := doc.Content[0]

	includes, err := extractIncludes(root)
	if err != nil {
		return nil, err
	}
	if len(includes) == 0 {
		return data, nil
	}

	merged := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for _, inc := range includes {
		incPath := inc
		if !filepath.IsAbs(incPath) {
			incPath = filepath.Join(baseDir, incPath)
		}
		incPath = filepath.Clean(incPath)
		if chain[incPath] {
			return nil, fmt.Errorf("circular include detected: %s", incPath)
		}
		incData, err := os.ReadFile(incPath)
		if err != nil {
			return nil, fmt.Errorf("reading include %s: %w", incPath, err)
		}
		next := make(map[string]bool, len(chain)+1)
		for k, v := range chain {
			next[k] = v
		}
		next[incPath] = true
		expanded, err := expandIncludes(incData, filepath.Dir(incPath), next)
		if err != nil {
			return nil, fmt.Errorf("in include %s: %w", incPath, err)
		}
		var incDoc yaml.Node
		if err := yaml.Unmarshal(expanded, &incDoc); err != nil {
			return nil, fmt.Errorf("parsing include %s: %w", incPath, err)
		}
		if len(incDoc.Content) > 0 && incDoc.Content[0].Kind == yaml.MappingNode {
			mergeMapping(merged, incDoc.Content[0])
		}
	}
	mergeMapping(merged, root)

	out := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{merged}}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(out); err != nil {
		return nil, fmt.Errorf("encoding merged config: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("encoding merged config: %w", err)
	}
	return buf.Bytes(), nil
}

// extractIncludes removes the `include` key from the mapping node and returns
// the list of include paths it declared.
func extractIncludes(root *yaml.Node) ([]string, error) {
	var includes []string
	kept := root.Content[:0]
	for i := 0; i+1 < len(root.Content); i += 2 {
		key, val := root.Content[i], root.Content[i+1]
		if key.Value != "include" {
			kept = append(kept, key, val)
			continue
		}
		switch val.Kind {
		case yaml.ScalarNode:
			includes = append(includes, val.Value)
		case yaml.SequenceNode:
			for _, item := range val.Content {
				if item.Kind != yaml.ScalarNode {
					return nil, fmt.Errorf("include entries must be strings, got %v", item.Kind)
				}
				includes = append(includes, item.Value)
			}
		default:
			return nil, fmt.Errorf("include must be a string or list of strings")
		}
	}
	root.Content = kept
	return includes, nil
}

// mergeMapping merges src into dst: matching mapping keys merge recursively,
// everything else is replaced by src.
func mergeMapping(dst, src *yaml.Node) {
	for i := 0; i+1 < len(src.Content); i += 2 {
		skey, sval := src.Content[i], src.Content[i+1]
		idx := -1
		for j := 0; j+1 < len(dst.Content); j += 2 {
			if dst.Content[j].Value == skey.Value {
				idx = j
				break
			}
		}
		if idx >= 0 && dst.Content[idx+1].Kind == yaml.MappingNode && sval.Kind == yaml.MappingNode {
			mergeMapping(dst.Content[idx+1], sval)
		} else if idx >= 0 {
			dst.Content[idx+1] = sval
		} else {
			dst.Content = append(dst.Content, skey, sval)
		}
	}
}
