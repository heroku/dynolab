//+build !linux

package exec

func (d *Dyno) start() error {
	return d.cmd.Start()
}

func (d *Dyno) reap() error {
	return nil
}
