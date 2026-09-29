package main

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

type fakeOwnedJob struct {
	calls            []string
	killErr, waitErr error
	timeout          time.Duration
}

func (j *fakeOwnedJob) Kill() error { j.calls = append(j.calls, "kill-owned-job"); return j.killErr }
func (j *fakeOwnedJob) WaitEmpty(d time.Duration) error {
	j.calls = append(j.calls, "wait-until-empty")
	j.timeout = d
	return j.waitErr
}
func TestReapOwnedJob(t *testing.T) {
	denied := errors.New("termination denied")
	notEmpty := errors.New("still active")
	for _, tc := range []struct {
		name string
		k, w error
		ok   bool
	}{{"normal", nil, nil, true}, {"natural-exit-won-race", denied, nil, true}, {"do-not-claim-success", nil, notEmpty, false}, {"preserve-both-errors", denied, notEmpty, false}} {
		t.Run(tc.name, func(t *testing.T) {
			j := &fakeOwnedJob{killErr: tc.k, waitErr: tc.w}
			err := reapOwnedJob(j, 15*time.Second)
			if (err == nil) != tc.ok {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(j.calls, []string{"kill-owned-job", "wait-until-empty"}) || j.timeout != 15*time.Second {
				t.Fatal(j)
			}
			if !tc.ok && (!errors.Is(err, tc.w) || (tc.k != nil && !errors.Is(err, tc.k))) {
				t.Fatal("lost cleanup error", err)
			}
		})
	}
}
func TestProgressLayout(t *testing.T) {
	if bodyFontSize != 18 || headingFontSize != 24 || smallFontSize != 14 {
		t.Fatal("unexpected compact font sizes")
	}
	if bodyFontSize <= 16 || bodyFontSize >= 22 {
		t.Fatal("body should be just slightly larger than original")
	}
	if progressClientWidth != 660 || progressClientHeight != 370 {
		t.Fatal("unexpected compact window")
	}
	if 524+120 > progressClientWidth || 320+32 > progressClientHeight || 480+124 > settingsClientWidth || 292+32 > settingsClientHeight {
		t.Fatal("buttons outside client area")
	}
	for _, v := range []struct{ dpi, w, h int32 }{{96, 1366, 728}, {120, 1366, 728}, {144, 1920, 1040}, {192, 1920, 1040}, {192, 2560, 1400}, {96, 800, 560}, {0, 1024, 728}} {
		d := fitUIDPI(v.dpi, v.w, v.h)
		if scaleUI(layoutFootprintWidth, d) > v.w-23 || scaleUI(layoutFootprintHeight, d) > v.h-23 {
			t.Fatalf("window outside work area: %+v dpi=%d", v, d)
		}
	}
	if fitUIDPI(192, 3840, 2160) != 120 {
		t.Fatal("high DPI must not balloon the compact utility window")
	}
}
func TestUIReleaseKeepsRuntimeCompatibility(t *testing.T) {
	if launcherVersion != "1.0.3" || wrapperVersion != "1.0.1" {
		t.Fatal("UI-only release must reuse the proven runtime adapter")
	}
}
