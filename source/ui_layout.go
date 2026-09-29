package main

const (
	bodyFontSize          int32 = 18
	headingFontSize       int32 = 24
	smallFontSize         int32 = 14
	progressClientWidth   int32 = 660
	progressClientHeight  int32 = 370
	settingsClientWidth   int32 = 620
	settingsClientHeight  int32 = 345
	layoutFootprintWidth  int32 = 760
	layoutFootprintHeight int32 = 470
)

func scaleUI(v, dpi int32) int32 { return (v*dpi + 48) / 96 }
func fitUIDPI(dpi, width, height int32) int32 {
	if dpi < 72 {
		dpi = 96
	}
	// A short-lived utility window should not balloon on high-DPI desktops.
	if dpi > 120 {
		dpi = 120
	}
	if width > 100 {
		if cap := (width - 24) * 96 / layoutFootprintWidth; cap < dpi {
			dpi = cap
		}
	}
	if height > 100 {
		if cap := (height - 24) * 96 / layoutFootprintHeight; cap < dpi {
			dpi = cap
		}
	}
	if dpi < 48 {
		dpi = 48
	}
	return dpi
}
