package lifecycle

import (
	"errors"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	logrus.SetLevel(logrus.FatalLevel)
	m.Run()
}

type Arr struct{}

func (*Arr) Close_() {}

func (*Arr) String() string { return "" }

func (*Arr) Step(stopCh <-chan struct{}) error {
	select {
	case <-stopCh:
		return &BreakError{}
	default:
		return nil
	}
}

type ErrArr struct{ Arr }

func (*ErrArr) Step(stopCh <-chan struct{}) error {
	select {
	case <-stopCh:
		return &BreakError{}
	default:
		return errors.New("")
	}
}

func TestFailsafeAsyncStart(t *testing.T) {
	t.Parallel()

	inst := &Arr{}
	manager := NewFailSafeAsyncManager[*Arr](inst)
	err := manager.Start(func(f *Arr) error { return nil })
	require.NoError(t, err)
}

func TestFailsafeAsyncErrorStart(t *testing.T) {
	t.Parallel()

	inst := &Arr{}
	manager := NewFailSafeAsyncManager[*Arr](inst)
	err := manager.Start(func(f *Arr) error { return errors.New("") })
	require.NoError(t, err)
	select {
	case <-manager.Done():
		t.FailNow()
	default:
	}
}

func TestFailsafeAsyncStartAfterStart(t *testing.T) {
	t.Parallel()

	inst := &Arr{}
	manager := NewFailSafeAsyncManager[*Arr](inst)
	err := manager.Start(func(f *Arr) error { return nil })
	require.NoError(t, err)
	err = manager.Start(func(f *Arr) error { return nil })
	require.NoError(t, err)
}

func TestFailsafeAsyncClose(t *testing.T) {
	t.Parallel()

	inst := &Arr{}
	manager := NewFailSafeAsyncManager[*Arr](inst)
	err := manager.Start(func(f *Arr) error { return nil })
	require.NoError(t, err)
	manager.Close()
}

func TestFailsafeAsyncCloseBeforeStart(t *testing.T) {
	t.Parallel()
	inst := &Arr{}
	manager := NewFailSafeAsyncManager[*Arr](inst)
	manager.Close()
}

func TestFailsafeAsyncStartAfterClose(t *testing.T) {
	t.Parallel()

	inst := &Arr{}
	manager := NewFailSafeAsyncManager[*Arr](inst)
	manager.Close()
	err := manager.Start(func(f *Arr) error { return nil })
	require.NoError(t, err)
}

func TestFailsafeAsyncStep(t *testing.T) {
	t.Parallel()

	inst := &ErrBar{
		Bar: Bar{},
	}
	manager := NewFailSafeAsyncManager[*ErrBar](inst)
	err := manager.Start(func(f *ErrBar) error { return nil })
	require.NoError(t, err)
	select {
	case <-manager.Done():
		t.FailNow()
	case <-time.After(time.Second):
	}
}
