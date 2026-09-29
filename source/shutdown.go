package main

import (
	"errors"
	"time"
)

type ownedProcessJob interface {
	Kill() error
	WaitEmpty(time.Duration) error
}

// Killing only the desktop root is insufficient: daemons and detached children
// may still hold the USB. Do not claim cleanup until the owned Job is empty.
func reapOwnedJob(job ownedProcessJob, timeout time.Duration) error {
	killErr := job.Kill()
	waitErr := job.WaitEmpty(timeout)
	if waitErr != nil {
		return errors.Join(killErr, waitErr)
	}
	// A concurrent natural exit may make termination redundant. Active==0 is the
	// authoritative completion condition; never enumerate/kill by executable name.
	return nil
}
