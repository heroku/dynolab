//+build !linux

package networking

import "errors"

// Setup is unsupported on this platform.
func (m *Monitor) Setup() error {
	return errors.New("networking: unsupported platform")
}
