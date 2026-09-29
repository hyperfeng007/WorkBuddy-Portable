package main

import "fmt"

// Readiness does not turn a later nonzero exit into a successful shutdown.
func desktopExitError(shown bool, err error) error {
	if err != nil {
		return err
	}
	if !shown {
		return fmt.Errorf("桌面在界面就绪前退出，请查看 Data\\Logs\\desktop.log")
	}
	return nil
}
