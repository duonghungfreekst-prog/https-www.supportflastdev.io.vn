package registry

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	if os.Getenv("TURNSTILE_ENABLED") == "" {
		os.Setenv("TURNSTILE_ENABLED", "false")
	}
	os.Exit(m.Run())
}
