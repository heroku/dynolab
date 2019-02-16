package supervisor

// Group manages the lifecycle of services and tasks. A task is a service that
// a nil error. It is identical to a github.com/oklog/run.Group except in the
// following ways:
//
// * the Start method replaces the Add method.
// * execute funcs are launched during the call to Start, instead of the call to Run.
// * returning a non-nil error from an execute func will not cause the other execute funcs to be interupted.
// * Start will return a non-nil error if an existing execute func has already returned a non-nil error.
// * execute funcs are interupted in the reverse order they are started.
// * if Start returns a non-nil error, all other executors have already been interupted.
type Group struct {
	actors []actor

	errc chan error
}

// Start runs an actor by launching the execute func registering it with the
// group. Each actor must be pre-emptable by an interrupt function. That is, if
// interrupt is invoked, execute should return.  Also, it must be safe to call
// interrupt even after execute has returned.
//
// The first actor (function) to return a non-nil error interrupts all running
// actors. The error is passed to the interrupt functions, and is returned by
// Run.
//
// If an actor added to the group has already triggered interrupts, the
// triggering error is returned by Start.
func (g *Group) Start(execute func() error, interrupt func(error)) error {
	if g.errc == nil {
		g.errc = make(chan error, 1)
	}

	select {
	case err := <-g.errc:
		g.interrupt(err)
		return err
	default:
	}

	act := actor{
		interrupt: interrupt,
		donec:     make(chan struct{}),
	}
	go act.run(execute, g.errc)
	g.actors = append(g.actors, act)

	return nil
}

// Run all actors (functions) concurrently.
// When an actor returns a non-nil error, all others are interrupted.
// Run only returns when all actors have exited.
// Run returns the error returned by the first exiting actor.
func (g *Group) Run() error {
	for _, a := range g.actors {
		select {
		case err := <-g.errc:
			g.interrupt(err)
			return err
		case <-a.donec:
		}
	}

	select {
	case err := <-g.errc:
		return err
	default:
		return nil
	}
}

func (g *Group) interrupt(err error) {
	for i := len(g.actors) - 1; i >= 0; i-- {
		a := g.actors[i]
		a.interrupt(err)
		<-a.donec
	}
}

type actor struct {
	interrupt func(error)
	donec     chan struct{}
}

func (a actor) run(execute func() error, errc chan<- error) {
	defer close(a.donec)

	if err := execute(); err != nil {
		select {
		case errc <- err:
		default:
		}
	}
}
