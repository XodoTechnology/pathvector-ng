package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
	return p
}

func unmarshal(t *testing.T, data []byte) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	require.NoError(t, yaml.Unmarshal(data, &m))
	return m
}

func TestExpandIncludesSingle(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "base.yml", "asn: 65001\nrouter-id: 192.0.2.1\n")
	main := writeFile(t, dir, "main.yml", "include: base.yml\npeers: {}\n")

	data, err := os.ReadFile(main)
	require.NoError(t, err)
	expanded, err := ExpandIncludes(data, dir)
	require.NoError(t, err)

	m := unmarshal(t, expanded)
	assert.Equal(t, 65001, m["asn"])
	assert.Equal(t, "192.0.2.1", m["router-id"])
	assert.Contains(t, m, "peers")
	assert.NotContains(t, m, "include")
}

func TestExpandIncludesListAndOverride(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.yml", "asn: 65001\npeers:\n  peer-a:\n    asn: 65002\n")
	writeFile(t, dir, "b.yml", "asn: 65009\npeers:\n  peer-b:\n    asn: 65003\n")
	main := writeFile(t, dir, "main.yml", "include:\n  - a.yml\n  - b.yml\npeers:\n  peer-a:\n    description: overridden\n")

	data, err := os.ReadFile(main)
	require.NoError(t, err)
	expanded, err := ExpandIncludes(data, dir)
	require.NoError(t, err)

	m := unmarshal(t, expanded)
	// Later include wins on scalar conflict.
	assert.Equal(t, 65009, m["asn"])
	// Maps merge recursively: peer-a from a.yml survives, gains main's key.
	peers := m["peers"].(map[string]interface{})
	assert.Contains(t, peers, "peer-b")
	peerA := peers["peer-a"].(map[string]interface{})
	assert.Equal(t, "overridden", peerA["description"])
	assert.Equal(t, 65002, peerA["asn"])
}

func TestExpandIncludesNested(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	writeFile(t, sub, "leaf.yml", "hostname: leaf\n")
	writeFile(t, dir, "mid.yml", "include: sub/leaf.yml\nbackend: frr\n")
	main := writeFile(t, dir, "main.yml", "include: mid.yml\nasn: 65001\n")

	data, err := os.ReadFile(main)
	require.NoError(t, err)
	expanded, err := ExpandIncludes(data, dir)
	require.NoError(t, err)

	m := unmarshal(t, expanded)
	assert.Equal(t, "leaf", m["hostname"])
	assert.Equal(t, "frr", m["backend"])
	assert.Equal(t, 65001, m["asn"])
}

func TestExpandIncludesCircular(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.yml", "include: b.yml\n")
	writeFile(t, dir, "b.yml", "include: a.yml\n")

	_, err := ExpandIncludes([]byte("include: a.yml\n"), dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "circular include")
}

func TestExpandIncludesMissing(t *testing.T) {
	dir := t.TempDir()
	_, err := ExpandIncludes([]byte("include: nope.yml\n"), dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading include")
}

func TestExpandIncludesNoInclude(t *testing.T) {
	data := []byte("asn: 65001\n")
	expanded, err := ExpandIncludes(data, t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, data, expanded)
}

func TestExpandIncludesNonMap(t *testing.T) {
	data := []byte("- just\n- a\n- list\n")
	expanded, err := ExpandIncludes(data, t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, data, expanded)
}

func TestExpandIncludesSequenceReplace(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "base.yml", "prefixes:\n  - 192.0.2.0/24\n")
	main := writeFile(t, dir, "main.yml", "include: base.yml\nprefixes:\n  - 198.51.100.0/24\n")

	data, err := os.ReadFile(main)
	require.NoError(t, err)
	expanded, err := ExpandIncludes(data, dir)
	require.NoError(t, err)

	m := unmarshal(t, expanded)
	assert.Equal(t, []interface{}{"198.51.100.0/24"}, m["prefixes"])
}
