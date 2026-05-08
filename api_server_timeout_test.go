package main

import (
	"testing"
	"time"
)

func TestAPITimeoutDefaultsAllowSlowWalletSends(t *testing.T) {
	if apiReadTimeout != 10*time.Second {
		t.Fatalf("unexpected API read timeout: %s", apiReadTimeout)
	}
	if apiIdleTimeout != 60*time.Second {
		t.Fatalf("unexpected API idle timeout: %s", apiIdleTimeout)
	}
	if apiWriteTimeout < 5*time.Minute {
		t.Fatalf("API write timeout too short for many-input wallet sends: %s", apiWriteTimeout)
	}
}
