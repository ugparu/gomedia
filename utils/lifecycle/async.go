package lifecycle

import (
	"errors"
	"runtime/debug"
	"sync"

	"github.com/ugparu/gomedia/utils/logger"
)

type asyncLifecycleManager[T AsyncInstance] struct {
	instance             T
	stopChan, doneChan   chan struct{}
	startOnce, closeOnce *sync.Once
}

func NewAsyncManager[T AsyncInstance](instance T) AsyncManager[T] {
	return &asyncLifecycleManager[T]{
		instance:  instance,
		stopChan:  make(chan struct{}),
		doneChan:  make(chan struct{}),
		startOnce: &sync.Once{},
		closeOnce: &sync.Once{},
	}
}

func (ssc *asyncLifecycleManager[T]) Start(startFunc func(T) error) (err error) {
	select {
	case <-ssc.stopChan:
		return &StartedAfterCloseError{}
	default:
		err = &StartedAlreadyError{}
	}
	ssc.startOnce.Do(func() {
		logger.Debugf(ssc.instance, "Starting async")
		if err = startFunc(ssc.instance); err != nil {
			close(ssc.doneChan)
			return
		}
		go ssc.process()
	})
	return err
}

func (ssc *asyncLifecycleManager[T]) process() {
	logger.Debug(ssc.instance, "Entering main loop")

	defer close(ssc.doneChan)
	running := true
	for running {
		func() {
			defer func() {
				if r := recover(); r != nil {
					logger.Errorf(ssc.instance, "Panic detected! Recovering from: %v", r)
					logger.Errorf(ssc.instance, "%s", debug.Stack())
					running = false
				}
			}()
			if err := ssc.instance.Step(ssc.stopChan); err != nil {
				if ok := errors.As(err, &errBreak); !ok {
					logger.Warningf(ssc.instance, "Detected error: %s", err.Error())
				}
				running = false
			}
		}()
	}
}

func (ssc *asyncLifecycleManager[T]) Close() {
	ssc.closeOnce.Do(func() {
		close(ssc.stopChan)
		ssc.startOnce.Do(func() {
			close(ssc.doneChan)
		})
		<-ssc.doneChan
		ssc.instance.Close_()
	})
}

func (ssc *asyncLifecycleManager[T]) Done() <-chan struct{} {
	return ssc.doneChan
}
