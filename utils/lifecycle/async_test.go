package lifecycle

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type Bar struct{}

func (*Bar) Close_() {}

func (*Bar) String() string { return "" }

func (*Bar) Step(stopCh <-chan struct{}) error {
	select {
	case <-stopCh:
		return &BreakError{}
	default:
		return nil
	}
}

type ErrBar struct{ Bar }

func (*ErrBar) Step(stopCh <-chan struct{}) error {
	select {
	case <-stopCh:
		return &BreakError{}
	default:
		return errors.New("")
	}
}

func TestAsyncStart(t *testing.T) {
	t.Parallel()

	inst := &Bar{}
	manager := NewAsyncManager[*Bar](inst)
	err := manager.Start(func(f *Bar) error { return nil })
	require.NoError(t, err)
}

func TestAsyncErrorStart(t *testing.T) {
	t.Parallel()

	inst := &Bar{}
	manager := NewAsyncManager[*Bar](inst)
	err := manager.Start(func(f *Bar) error { return errors.New("") })
	require.Error(t, err)
	select {
	case <-manager.Done():
	default:
		t.FailNow()
	}
}

func TestAsyncStartAfterStart(t *testing.T) {
	t.Parallel()

	inst := &Bar{}
	manager := NewAsyncManager[*Bar](inst)
	err := manager.Start(func(f *Bar) error { return nil })
	require.NoError(t, err)
	err = manager.Start(func(f *Bar) error { return nil })
	targetError := &StartedAlreadyError{}
	require.ErrorAs(t, err, &targetError)
}

func TestAsyncClose(t *testing.T) {
	t.Parallel()

	inst := &Bar{}
	manager := NewAsyncManager[*Bar](inst)
	err := manager.Start(func(f *Bar) error { return nil })
	require.NoError(t, err)
	manager.Close()
}

func TestAsyncCloseBeforeStart(t *testing.T) {
	t.Parallel()
	inst := &Bar{}
	manager := NewAsyncManager[*Bar](inst)
	manager.Close()
}

func TestAsyncStartAfterClose(t *testing.T) {
	t.Parallel()

	inst := &Bar{}
	manager := NewAsyncManager[*Bar](inst)
	manager.Close()
	err := manager.Start(func(f *Bar) error { return nil })
	targetError := &StartedAfterCloseError{}
	require.ErrorAs(t, err, &targetError)
}

func TestAsyncStep(t *testing.T) {
	t.Parallel()

	inst := &ErrBar{
		Bar: Bar{},
	}
	manager := NewAsyncManager[*ErrBar](inst)
	err := manager.Start(func(f *ErrBar) error { return nil })
	require.NoError(t, err)
	select {
	case <-manager.Done():
	case <-time.After(time.Second):
		t.FailNow()
	}
}
