package lifecycle

import (
	"errors"
	"runtime/debug"
	"sync"

	"github.com/ugparu/gomedia/utils/logger"
)

type failsafeAsyncLifecycleManager[T AsyncInstance] struct {
	instance             T
	stopChan, doneChan   chan struct{}
	startOnce, closeOnce *sync.Once
}

func NewFailSafeAsyncManager[T AsyncInstance](instance T) AsyncManager[T] {
	return &failsafeAsyncLifecycleManager[T]{
		instance:  instance,
		stopChan:  make(chan struct{}),
		doneChan:  make(chan struct{}),
		startOnce: &sync.Once{},
		closeOnce: &sync.Once{},
	}
}

func (ssc *failsafeAsyncLifecycleManager[T]) Start(startFunc func(T) error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			logger.Errorf(ssc.instance, "Panic detected! Recovering from: %v", r)
			logger.Errorf(ssc.instance, "%s", debug.Stack())
		}
	}()

	ssc.startOnce.Do(func() {
		logger.Debugf(ssc.instance, "Starting failsafe async")
		if err = startFunc(ssc.instance); err != nil {
			logger.Warningf(ssc.instance, "Detected error ons start: %s", err.Error())
		}
		go ssc.process()
	})
	return nil
}

func (ssc *failsafeAsyncLifecycleManager[T]) process() {
	logger.Debug(ssc.instance, "Entering main loop")

	defer close(ssc.doneChan)
	running := true
	for running {
		func() {
			defer func() {
				if r := recover(); r != nil {
					logger.Errorf(ssc.instance, "Panic detected! Recovering from: %v", r)
					logger.Errorf(ssc.instance, "%s", debug.Stack())
				}
			}()
			if err := ssc.instance.Step(ssc.stopChan); err != nil {
				if ok := errors.As(err, &errBreak); ok {
					running = false
				} else {
					logger.Warningf(ssc.instance, "Detected error: %s", err.Error())
				}
			}
		}()
	}
}

func (ssc *failsafeAsyncLifecycleManager[T]) Close() {
	ssc.closeOnce.Do(func() {
		close(ssc.stopChan)
		ssc.startOnce.Do(func() {
			close(ssc.doneChan)
		})
		<-ssc.doneChan
		ssc.instance.Close_()
	})
}

func (ssc *failsafeAsyncLifecycleManager[T]) Done() <-chan struct{} {
	return ssc.doneChan
}
