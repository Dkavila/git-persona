package config

import "errors"

// Sentinel errors for the configuration layer. Callers compare with errors.Is,
// so every return site is free to wrap these with additional context.
var (
	// ErrInvalidProfile signals that a profile failed field validation.
	ErrInvalidProfile = errors.New("invalid profile")

	// ErrDuplicateProfile signals that a profile with the same name already
	// exists. Comparison is case-insensitive.
	ErrDuplicateProfile = errors.New("profile already exists")

	// ErrProfileNotFound signals that no profile matches the given name.
	ErrProfileNotFound = errors.New("profile not found")

	// ErrCorruptStore signals that the on-disk store exists but could not be
	// decoded. It is kept distinct from a missing file, which is not an error.
	ErrCorruptStore = errors.New("corrupt profile store")
)
