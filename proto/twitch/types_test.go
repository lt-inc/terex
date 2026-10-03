package twitch

import "testing"

func TestDefaultSettingsAndValidation(t *testing.T) {
	s := DefaultSettings()
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	s.Enabled = true
	if s.Validate() == nil {
		t.Fatal("enabled collector without target")
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
