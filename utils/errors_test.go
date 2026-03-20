package utils

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTryAgainError(t *testing.T) {
	t.Parallel()
	var err error = TryAgainError{}
	assert.Equal(t, "Try again", err.Error())

	var target TryAgainError
	require.True(t, errors.As(err, &target))
}

func TestUnimplementedError(t *testing.T) {
	t.Parallel()
	var err error = UnimplementedError{}
	assert.Equal(t, "Not implemented", err.Error())

	var target UnimplementedError
	require.True(t, errors.As(err, &target))
}

func TestNoCodecDataError(t *testing.T) {
	t.Parallel()
	var err error = NoCodecDataError{}
	assert.Equal(t, "No codec data", err.Error())

	var target NoCodecDataError
	require.True(t, errors.As(err, &target))
}

func TestNilPacketError(t *testing.T) {
	t.Parallel()
	var err error = NilPacketError{}
	assert.Equal(t, "nil packet", err.Error())

	var target NilPacketError
	require.True(t, errors.As(err, &target))
}

func TestErrorTypes_NotConfused(t *testing.T) {
	t.Parallel()
	var err error = TryAgainError{}

	var target NilPacketError
	assert.False(t, errors.As(err, &target), "TryAgainError should not match NilPacketError")
}
