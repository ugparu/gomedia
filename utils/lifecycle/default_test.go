package lifecycle

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type Foo struct{}

func (*Foo) Close_() {}

func (*Foo) String() string { return "" }

func TestStart(t *testing.T) {
	t.Parallel()

	inst := &Foo{}
	manager := NewDefaultManager[*Foo](inst)
	err := manager.Start(func(f *Foo) error { return nil })
	require.NoError(t, err)
}

func TestErrorStart(t *testing.T) {
	t.Parallel()

	inst := &Foo{}
	manager := NewDefaultManager[*Foo](inst)
	err := manager.Start(func(f *Foo) error { return errors.New("") })
	require.Error(t, err)
}

func TestStartAfterStart(t *testing.T) {
	t.Parallel()

	inst := &Foo{}
	manager := NewDefaultManager[*Foo](inst)
	err := manager.Start(func(f *Foo) error { return nil })
	require.NoError(t, err)
	err = manager.Start(func(f *Foo) error { return nil })
	targetError := &StartedAlreadyError{}
	require.ErrorAs(t, err, &targetError)
}

func TestClose(t *testing.T) {
	t.Parallel()

	inst := &Foo{}
	manager := NewDefaultManager[*Foo](inst)
	err := manager.Start(func(f *Foo) error { return nil })
	require.NoError(t, err)
	manager.Close()
}

func TestCloseBeforeStart(t *testing.T) {
	t.Parallel()

	inst := &Foo{}
	manager := NewDefaultManager[*Foo](inst)
	manager.Close()
}

func TestStartAfterClose(t *testing.T) {
	t.Parallel()

	inst := &Foo{}
	manager := NewDefaultManager[*Foo](inst)
	manager.Close()
	err := manager.Start(func(f *Foo) error { return nil })
	targetError := &StartedAfterCloseError{}
	require.ErrorAs(t, err, &targetError)
}
