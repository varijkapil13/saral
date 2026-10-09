package form

import (
	"time"

	appissue "github.com/varijkapil13/saral/internal/app/issue"
)

// schemas is the create-screen cache every form in this session shares.
var schemas = appissue.NewSchemas(appissue.SchemaTTL, time.Now)
