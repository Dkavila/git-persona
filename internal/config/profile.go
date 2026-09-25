package config

import (
	"fmt"
	"net/mail"
	"strings"
	"time"
)

// pathSeparators are rejected in profile names because the name is used to
// derive the SSH key filename; a separator would let that path escape ~/.ssh.
const pathSeparators = `/\`

// Profile is a single Git identity: the name and email written to the Git
// config, plus the ed25519 key that backs its SSH authentication.
type Profile struct {
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	KeyPath   string    `json:"key_path"`
	CreatedAt time.Time `json:"created_at"`
}

// Validate reports whether the profile is well formed. Every failure wraps
// ErrInvalidProfile.
func (p Profile) Validate() error {
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("%w: name must not be empty", ErrInvalidProfile)
	}
	if strings.ContainsAny(p.Name, pathSeparators) {
		return fmt.Errorf("%w: name %q must not contain a path separator", ErrInvalidProfile, p.Name)
	}
	if strings.TrimSpace(p.Email) == "" {
		return fmt.Errorf("%w: email must not be empty", ErrInvalidProfile)
	}

	// ParseAddress also accepts the display form "Name <a@b.com>"; comparing
	// the parsed address back to the input rejects anything but a bare address.
	addr, err := mail.ParseAddress(p.Email)
	if err != nil || addr.Address != p.Email {
		return fmt.Errorf("%w: email %q is not a valid address", ErrInvalidProfile, p.Email)
	}
	return nil
}
