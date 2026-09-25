package mention

import (
	"os"
	"testing"

	"github.com/varijkapil13/saral/internal/testsupport"
)

func TestMain(m *testing.M) { os.Exit(testsupport.IsolateDirs(m)) }
