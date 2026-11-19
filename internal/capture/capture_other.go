//go:build !linux

package capture

// New opens a capturer for display index i.
func New(display int) (Capturer, error) { return newGeneric(display) }

// Preflight checks OS permissions; see the darwin implementation.
func Preflight() error { return preflight() }
