package wire

// FIRE: bool with omitempty — a false can never be sent, so a meaningful
// false is dropped from the wire.
type Receipt struct {
	Delivered bool `json:"delivered,omitempty"` // want `omitempty on bool field`
	Retry     bool `yaml:"retry,omitempty"`     // want `omitempty on bool field`
}

// SILENT: bool without omitempty — false marshals correctly.
type Good struct {
	Delivered bool `json:"delivered"`
}

// SILENT: *bool with omitempty is the correct fix (nil = absent, false = sent).
type GoodPtr struct {
	Delivered *bool `json:"delivered,omitempty"`
}

// SILENT: int with omitempty — out of scope (zero is often genuinely absent).
type Paged struct {
	Page int `json:"page,omitempty"`
}

// SILENT: string with omitempty — the classic legitimate use.
type Named struct {
	Name string `json:"name,omitempty"`
}

// SILENT: the word "omitempty" in a non-marshal tag must not fire.
type Validated struct {
	Flag bool `validate:"omitempty"`
}

// SILENT: a deliberate absent-equals-false encoding, silenced with a reason.
type silencedTrailing struct {
	Flag bool `json:"flag,omitempty"` //nolint:omitemptyscalar // absent means false on this wire
}

type silencedDoc struct {
	//nolint:omitemptyscalar // absent means false on this wire
	Flag bool `json:"flag,omitempty"`
}

// SILENT: the marker golangci-lint does not read, with a reason.
type ignoredTrailing struct {
	Flag bool `json:"flag,omitempty"` //omitemptyscalar:ignore absent means false on this wire
}

type ignoredDoc struct {
	//omitemptyscalar:ignore absent means false on this wire
	Flag bool `json:"flag,omitempty"`
}

// FLAGGED: the ignore marker with no reason silences nothing.
type ignoredNoReason struct {
	//omitemptyscalar:ignore
	Flag bool `json:"flag,omitempty"` // want `omitempty on bool field`
}
