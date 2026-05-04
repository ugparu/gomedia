package utils

// TryAgainError signals a transient failure — the caller should retry the same operation.
type TryAgainError struct{}

func (TryAgainError) Error() string {
	return "Try again"
}

// UnimplementedError marks an interface method that has no implementation for the current codec or container.
type UnimplementedError struct{}

func (UnimplementedError) Error() string {
	return "Not implemented"
}

// NoCodecDataError is returned when a muxer or writer is asked to process a packet
// before the demuxer has delivered codec parameters.
type NoCodecDataError struct{}

func (NoCodecDataError) Error() string {
	return "No codec data"
}

// NilPacketError signals that a nil packet reached a stage that cannot handle one.
type NilPacketError struct{}

func (NilPacketError) Error() string {
	return "nil packet"
}
