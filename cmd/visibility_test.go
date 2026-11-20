package cmd

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFetchRoutingStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/data/routing-status/data.json", r.URL.Path)
		assert.Equal(t, "1.1.1.0/24", r.URL.Query().Get("resource"))
		fmt.Fprint(w, `{"status":"ok","data":{"resource":"1.1.1.0/24","visibility":{"v4":{"ris_peers_seeing":317,"total_ris_peers":318},"v6":{"ris_peers_seeing":0,"total_ris_peers":0}},"origins":[{"origin":13335,"route_objects":["APNIC"]}]}}`)
	}))
	defer srv.Close()

	old := ripeStatBase
	ripeStatBase = srv.URL
	defer func() { ripeStatBase = old }()

	st, err := fetchRoutingStatus(srv.Client(), "1.1.1.0/24")
	require.NoError(t, err)
	assert.Equal(t, 317, st.Data.Visibility.V4.Seeing)
	assert.Equal(t, 318, st.Data.Visibility.V4.Total)
	require.Len(t, st.Data.Origins, 1)
	assert.Equal(t, 13335, st.Data.Origins[0].Origin)
}

func TestFetchRoutingStatusHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	old := ripeStatBase
	ripeStatBase = srv.URL
	defer func() { ripeStatBase = old }()

	_, err := fetchRoutingStatus(srv.Client(), "1.1.1.0/24")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 502")
}
