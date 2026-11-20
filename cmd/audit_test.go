package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/XodoTechnology/pathvector-ng/pkg/config"
)

func TestOriginatedPrefixes(t *testing.T) {
	c := &config.Config{
		Prefixes: []string{"192.0.2.0/24", "2001:db8::/32", "not-a-prefix"},
		Kernel: &config.Kernel{
			Statics: map[string]string{
				"198.51.100.0/24": "192.0.2.1",
				"192.0.2.0/24":    "10.0.0.1", // duplicate of prefixes entry
				"bad":             "10.0.0.2",
			},
		},
	}
	got := originatedPrefixes(c)
	var strs []string
	for _, p := range got {
		strs = append(strs, p.String())
	}
	assert.Equal(t, []string{"192.0.2.0/24", "198.51.100.0/24", "2001:db8::/32"}, strs)
}

func TestOriginatedPrefixesNilKernel(t *testing.T) {
	c := &config.Config{Prefixes: []string{"192.0.2.0/24"}}
	got := originatedPrefixes(c)
	assert.Len(t, got, 1)
}
