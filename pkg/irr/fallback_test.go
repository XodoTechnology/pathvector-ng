package irr

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeBGPQ4Selective installs a fake bgpq4 that fails when the invocation
// contains failMatch and otherwise runs body. It returns the invocation log.
func fakeBGPQ4Selective(t *testing.T, failMatch, body string) string {
	t.Helper()
	dir := t.TempDir()
	logFile := filepath.Join(dir, "args.log")
	script := filepath.Join(dir, "bgpq4")
	content := "#!/bin/sh\necho \"$@\" >> " + logFile + "\n" +
		"case \"$*\" in\n  *" + failMatch + "*) exit 1;;\nesac\n" + body + "\n"
	//nolint:gosec
	require.NoError(t, os.WriteFile(script, []byte(content), 0755))

	orig := bgpq4Command
	bgpq4Command = script
	t.Cleanup(func() { bgpq4Command = orig })
	return logFile
}

func TestASMembersServerFallback(t *testing.T) {
	logFile := fakeBGPQ4Selective(t, "bad-irr", `echo '{"NN": [64512]}'`)

	members, err := ASMembers("AS-EXAMPLE", []string{"bad-irr.example", "good-irr.example"}, 5, "")
	require.NoError(t, err)
	assert.Equal(t, []uint32{64512}, members)

	// The bad server is tried first, then the good one
	inv := invocations(t, logFile)
	require.Len(t, inv, 2)
	assert.Contains(t, inv[0], "-h bad-irr.example")
	assert.Contains(t, inv[1], "-h good-irr.example")
}

func TestPrefixSetServerFallback(t *testing.T) {
	logFile := fakeBGPQ4Selective(t, "bad-irr", `printf 'define X = [\n    192.0.2.0/24\n];\n'`)

	prefixes, err := PrefixSet("AS-EXAMPLE", 4, []string{"bad-irr.example", "good-irr.example"}, 5, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"192.0.2.0/24"}, prefixes)

	inv := invocations(t, logFile)
	require.Len(t, inv, 2)
	assert.Contains(t, inv[0], "-h bad-irr.example")
	assert.Contains(t, inv[1], "-h good-irr.example")
}

func TestASMembersAllServersFail(t *testing.T) {
	fakeBGPQ4Selective(t, "-h", `exit 1`) // every server fails

	_, err := ASMembers("AS-EXAMPLE", []string{"one.example", "two.example"}, 5, "")
	require.Error(t, err)
	// The error should name both attempted servers
	assert.Contains(t, err.Error(), "one.example")
	assert.Contains(t, err.Error(), "two.example")
}

func TestForEachServerOrder(t *testing.T) {
	// Servers are tried in order; the first success stops the iteration
	var tried []string
	err := forEachServer([]string{"a", "b", "c"}, func(s string) error {
		tried = append(tried, s)
		return fmt.Errorf("fail %s", s)
	})
	require.Error(t, err)
	assert.Equal(t, []string{"a", "b", "c"}, tried)
	assert.Contains(t, err.Error(), "a")
	assert.Contains(t, err.Error(), "c")

	// Success on the second server stops before the third
	tried = nil
	err = forEachServer([]string{"a", "b", "c"}, func(s string) error {
		tried = append(tried, s)
		if s == "b" {
			return nil
		}
		return fmt.Errorf("fail %s", s)
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b"}, tried)

	// Empty list still makes one attempt
	tried = nil
	err = forEachServer(nil, func(s string) error {
		tried = append(tried, s)
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, []string{""}, tried)
}

func TestQueryServerFallback(t *testing.T) {
	servers := []string{"down.example", "up.example"}
	calls := 0
	v, err := queryServer(servers, func(s string) (string, error) {
		calls++
		if s == "down.example" {
			return "", fmt.Errorf("connection refused")
		}
		return "answer-from-" + s, nil
	})
	require.NoError(t, err)
	assert.Equal(t, "answer-from-up.example", v)
	assert.Equal(t, 2, calls)

	// All fail -> zero value + joined error
	v, err = queryServer(servers, func(s string) (string, error) {
		return "", fmt.Errorf("down: %s", s)
	})
	require.Error(t, err)
	assert.Equal(t, "", v)
	assert.Contains(t, err.Error(), "down.example")
	assert.Contains(t, err.Error(), "up.example")
}

func TestVerifyPolicyServerFallback(t *testing.T) {
	fakeBGPQ4(t, `echo '{"NN": [924, 6939]}'`)

	// First server is unreachable (closed port), second is the fake whois
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	closedAddr := l.Addr().String()
	require.NoError(t, l.Close())

	goodAddr, _ := fakeWhois(t, autNumUpstreamsSet)

	missing, err := VerifyPolicy(207960, 6939, false, true, []string{closedAddr, goodAddr}, 5, "")
	require.NoError(t, err)
	assert.Empty(t, missing)
}
