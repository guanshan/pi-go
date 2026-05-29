package compaction

type Settings struct {
	Enabled          bool
	ReserveTokens    int
	KeepRecentTokens int
	MaxTokens        int
	SummaryMaxChars  int
}

var DefaultSettings = Settings{
	Enabled:          true,
	ReserveTokens:    16384,
	KeepRecentTokens: 20000,
	MaxTokens:        120000,
	SummaryMaxChars:  6000,
}

func withDefaults(settings Settings) Settings {
	if settings == (Settings{}) {
		return DefaultSettings
	}
	if settings.ReserveTokens <= 0 {
		settings.ReserveTokens = DefaultSettings.ReserveTokens
	}
	if settings.KeepRecentTokens <= 0 {
		settings.KeepRecentTokens = DefaultSettings.KeepRecentTokens
	}
	if settings.MaxTokens <= 0 {
		settings.MaxTokens = DefaultSettings.MaxTokens
	}
	if settings.SummaryMaxChars <= 0 {
		settings.SummaryMaxChars = DefaultSettings.SummaryMaxChars
	}
	return settings
}
