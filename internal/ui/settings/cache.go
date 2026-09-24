package settings

import (
	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/ui/kernel"
)

func init() { kernel.RegisterSetting(clearCacheSetting()) }

// clearCacheSetting sits beside session.memory: that one forgets how views
// were left, this one what the site answered.
func clearCacheSetting() kernel.Setting {
	return kernel.Setting{
		ID:      "session.cache",
		Section: sessionSection,
		Order:   4,
		Title:   "Clear the cache",
		Summary: "every issue, search, board and backlog this profile keeps on disk; the next read fetches them again",
		Kind:    kernel.KindAction,
		Scope:   kernel.ScopeMachine,
		Run:     clearCache,
	}
}

func clearCache(d kernel.Deps) tea.Cmd {
	held, ok := d.Cache.(app.CacheClearer)
	if !ok {
		return kernel.Warn("this session keeps no cache, so there is nothing to clear")
	}
	return func() tea.Msg {
		if err := held.Clear(); err != nil {
			return kernel.StatusMsg{Text: "the cache could not be cleared: " + err.Error(), Level: kernel.LevelWarn}
		}
		return kernel.StatusMsg{Text: "cache cleared — what is on screen stays until it is read again", Level: kernel.LevelInfo}
	}
}
