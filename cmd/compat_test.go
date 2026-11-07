package cmd

import "testing"

func TestNormalizeSemver(t *testing.T) {
	cases := map[string]string{
		"2.14":                   "v2.14",
		"v2.0.7":                 "v2.0.7",
		"10.2.6":                 "v10.2.6",
		"0.1.1+git7129.83a42fb0": "v0.1.1+git7129.83a42fb0",
		"3.4.0-rc1":              "v3.4.0-rc1",
		"not-a-version":          "",
		"":                       "",
		"  2.15.1  ":             "v2.15.1",
	}
	for in, want := range cases {
		if got := normalizeSemver(in); got != want {
			t.Errorf("normalizeSemver(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCompatStatus(t *testing.T) {
	// bird range v2.0.7 - v3.4.0
	cases := []struct {
		detected, minV, maxV, want string
	}{
		{"2.14", "2.0.7", "3.4.0", "supported"},
		{"2.0.7", "2.0.7", "3.4.0", "supported"},
		{"3.4.0", "2.0.7", "3.4.0", "supported"},
		{"2.0.6", "2.0.7", "3.4.0", "older-than-tested"},
		{"1.6.8", "2.0.7", "3.4.0", "older-than-tested"},
		{"3.5.0", "2.0.7", "3.4.0", "newer-than-tested"},
		{"4.0.0", "2.0.7", "3.4.0", "newer-than-tested"},
		{"0.1.1+git7129", "0.1.1", "0.1.1", "supported"},
		{"0.2.0", "0.1.1", "0.1.1", "newer-than-tested"},
		{"garbage", "2.0.7", "3.4.0", "unknown"},
		{"", "2.0.7", "3.4.0", "unknown"},
	}
	for _, tc := range cases {
		if got := compatStatus(tc.detected, tc.minV, tc.maxV); got != tc.want {
			t.Errorf("compatStatus(%q,%q,%q) = %q, want %q",
				tc.detected, tc.minV, tc.maxV, got, tc.want)
		}
	}
}
