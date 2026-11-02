package opencloud

import "log/slog"

// Secret is a credential. It prints, logs and marshals as hidden; only Reveal gives it.
type Secret string

const hidden = "[hidden]"

// String hides the secret from fmt.
func (Secret) String() string { return hidden }

// GoString hides the secret from %#v.
func (Secret) GoString() string { return hidden }

// LogValue hides the secret from slog.
func (Secret) LogValue() slog.Value { return slog.StringValue(hidden) }

// MarshalJSON hides the secret from encoding/json.
func (Secret) MarshalJSON() ([]byte, error) { return []byte(`"` + hidden + `"`), nil }

// Reveal is the credential itself, for the request to OpenCloud only.
func (s Secret) Reveal() string { return string(s) }
