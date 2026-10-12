package twitch

import "testing"

func TestDefaultSettingsAndValidation(t *testing.T) {
	s := DefaultSettings()
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	s.Enabled = true
	if err := s.Validate(); err != nil {
		t.Fatal("shared collector settings do not choose flow destinations", err)
	}
	s.TargetAccountID = 1
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	s.Timezone = "unknown"
	if s.Validate() == nil {
		t.Fatal("invalid timezone")
	}
}

func TestUnlimitedDownloadsDoNotExhaustAndKeepOptionalCaps(t *testing.T) {
	s := DefaultSettings()
	if !s.DownloadsUnlimited() || s.DailyDownloadLimitReached(10000) || s.Validate() != nil {
		t.Fatal("unlimited defaults are blocked")
	}
	s.DailyDownloads = 3
	if s.DownloadsUnlimited() || s.DailyDownloadLimitReached(2) || !s.DailyDownloadLimitReached(3) || s.RemainingDownloads(2) != 1 || s.Validate() != nil {
		t.Fatal("legacy cap no longer works")
	}
	s.DailyDownloads = -1
	if s.Validate() == nil {
		t.Fatal("negative daily cap accepted")
	}
}
