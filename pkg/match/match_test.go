package match

import "testing"

func TestCommonIXs(t *testing.T) {
	if _, err := CommonIXs(34553, 112, false, 10, ""); err != nil {
		t.Error(err)
	}
}
