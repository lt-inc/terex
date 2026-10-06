package config

import (
	"testing"
	"time"
)

func TestDatabaseStartupTimeout(t *testing.T) {
	for _, test := range []struct {
		value string
		want  time.Duration
	}{{"", time.Minute}, {"15s", 15 * time.Second}, {"0s", 0}, {"-1s", 0}, {"invalid", 0}} {
		t.Setenv("DB_STARTUP_TIMEOUT", test.value)
		if got := Load().DBStartupTimeout; got != test.want {
			t.Fatalf("DB_STARTUP_TIMEOUT %q: got %s want %s", test.value, got, test.want)
		}
	}
}
