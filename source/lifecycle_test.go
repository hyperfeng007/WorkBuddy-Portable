package main

import (
	"errors"
	"testing"
)

func TestDesktopCrashNeverReportedAsSuccess(t *testing.T) {
	crash := errors.New("0xC0000005 access violation")
	if desktopExitError(true, crash) != crash || desktopExitError(false, crash) != crash {
		t.Fatal("lost crash status")
	}
	if desktopExitError(false, nil) == nil {
		t.Fatal("early exit accepted")
	}
	if desktopExitError(true, nil) != nil {
		t.Fatal("clean exit rejected")
	}
}
