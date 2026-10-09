package move

import (
	"testing"

	appmove "github.com/varijkapil13/saral/internal/app/move"
)

func TestTooMany_RefusesAboveTheEndpointsCapAndSaysBothNumbers(t *testing.T) {
	t.Parallel()
	if _, over := tooMany(appmove.MaxKeys); over {
		t.Errorf("%d issues was refused and the endpoint takes it", appmove.MaxKeys)
	}
	reason, over := tooMany(appmove.MaxKeys + 1)
	if !over {
		t.Fatalf("%d issues was accepted and the endpoint takes %d", appmove.MaxKeys+1, appmove.MaxKeys)
	}
	mustContain(t, reason, "1000", "1001")
}
