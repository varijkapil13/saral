package timeline

import (
	"github.com/varijkapil13/saral/internal/config"
)

// configuredFields are the field names this profile puts at the top of the date
// cascade. They come out of the file rather than out of Deps, which carries no
// profile — and only when the active profile is on the site this session is
// talking to, because a session started with --profile would otherwise take
// another account's field names.
func configuredFields(site string) (start, end []string) {
	path, err := config.Path()
	if err != nil {
		return nil, nil
	}
	cfg, err := config.LoadFile(path)
	if err != nil {
		return nil, nil
	}
	profile, err := cfg.Current()
	if err != nil {
		return nil, nil
	}
	if site != "" && profile.Site != site {
		return nil, nil
	}
	return profile.Timeline.Start, profile.Timeline.End
}
