package utils

// TryAgainError represents an error indicating that the operation should be retried.
type TryAgainError struct {
}

// Error returns the error message for TryAgainError.
func (TryAgainError) Error() string {
	return "Try again"
}

// UnimplementedError represents an error indicating that the operation is not implemented.
type UnimplementedError struct {
}

// Error returns the error message for UnimplementedError.
func (UnimplementedError) Error() string {
	return "Not implemented"
}

// NoCodecDataError represents an error indicating that the no codec data was provided.
type NoCodecDataError struct {
}

// Error returns the error message for NoCodecDataError.
func (NoCodecDataError) Error() string {
	return "No codec data"
}

// NoCodecDataError represents an error indicating that provided packet is nil.
type NilPacketError struct {
}

// Error method implementation for NilPacketError.
func (NilPacketError) Error() string {
	return "nil packet"
}
