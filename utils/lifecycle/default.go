package lifecycle

import (
	"sync"

	"github.com/ugparu/gomedia/utils/logger"
)

type defaultLifecycleManager[T Instance] struct {
	instance             T
	log                  logger.Logger
	startOnce, closeOnce *sync.Once
	closeChan            chan struct{}
}

func NewDefaultManager[T Instance](instance T, log logger.Logger) Manager[T] {
	return &defaultLifecycleManager[T]{
		instance:  instance,
		log:       log,
		closeChan: make(chan struct{}),
		startOnce: &sync.Once{},
		closeOnce: &sync.Once{},
	}
}

func (ssc *defaultLifecycleManager[T]) Start(startFunc func(T) error) (err error) {
	select {
	case <-ssc.closeChan:
		return &StartedAfterCloseError{}
	default:
		err = &StartedAlreadyError{}
	}
	ssc.startOnce.Do(func() {
		ssc.log.Debugf(ssc.instance, "Starting default")
		if err = startFunc(ssc.instance); err != nil {
			return
		}
	})
	return err
}

func (ssc *defaultLifecycleManager[T]) Close() {
	ssc.closeOnce.Do(func() {
		ssc.instance.Close_()
		close(ssc.closeChan)
	})
}
