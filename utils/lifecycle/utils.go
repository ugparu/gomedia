package lifecycle

type Instance interface {
	Close_()
	String() string
}

type AsyncInstance interface {
	Instance
	Step(stopChan <-chan struct{}) error
}

type Manager[T Instance] interface {
	Start(func(T) error) error
	Close()
}

type AsyncManager[T AsyncInstance] interface {
	Manager[T]
	Done() <-chan struct{}
}

type BreakError struct{}

func (*BreakError) Error() string {
	return "break"
}

type StartedAlreadyError struct{}

func (*StartedAlreadyError) Error() string {
	return "started already"
}

type StartedAfterCloseError struct{}

func (*StartedAfterCloseError) Error() string {
	return "start after close"
}

var errBreak = &BreakError{}
