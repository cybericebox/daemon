// Package secretModel defines opaque references and immutable envelope
// metadata for values that must never enter a signal or notification payload.
package secretModel

import (
	"encoding/json"
	"time"

	"github.com/gofrs/uuid"
)

// KeyPurpose selects an independently configured master key domain.
type KeyPurpose string

const (
	PurposeNotification KeyPurpose = "notification"
	PurposeExercise     KeyPurpose = "exercise"
)

// Reference is safe to persist in a signal or dispatch payload. It never
// carries plaintext or encrypted bytes.
type Reference struct {
	ID        uuid.UUID  `json:"id"`
	Purpose   KeyPurpose `json:"purpose"`
	FieldPath string     `json:"field_path"`
}

// Value carries plaintext only while a typed signal is being built. Its JSON
// form is either a plaintext string before publication or an opaque Reference
// after SignalPublisher seals it. Fields of this type must be tagged
// `secret:"true"` in signal payloads.
type Value struct {
	plaintext string
	ref       *Reference
}

func NewValue(plaintext string) Value { return Value{plaintext: plaintext} }

func (v Value) Plaintext() string { return v.plaintext }

func (v Value) Reference() *Reference {
	if v.ref == nil {
		return nil
	}
	copy := *v.ref
	return &copy
}

func (v *Value) SetReference(ref Reference) {
	v.plaintext = ""
	v.ref = &ref
}

func (v Value) MarshalJSON() ([]byte, error) {
	if v.ref != nil {
		return json.Marshal(v.ref)
	}
	return json.Marshal(v.plaintext)
}

func (v *Value) UnmarshalJSON(data []byte) error {
	var plaintext string
	if err := json.Unmarshal(data, &plaintext); err == nil {
		v.plaintext = plaintext
		v.ref = nil
		return nil
	}
	var ref Reference
	if err := json.Unmarshal(data, &ref); err != nil {
		return err
	}
	v.plaintext = ""
	v.ref = &ref
	return nil
}

// Envelope contains encrypted secret material and the immutable metadata used
// as authenticated encryption context. Ciphertext and wrapped key are opaque
// to callers; only the secret store may decrypt them.
type Envelope struct {
	ID              uuid.UUID  `json:"id"`
	Purpose         KeyPurpose `json:"purpose"`
	KeyVersion      int32      `json:"key_version"`
	SignalType      string     `json:"signal_type"`
	FieldPath       string     `json:"field_path"`
	ScopeEventID    uuid.UUID  `json:"scope_event_id"`
	RecipientUserID *uuid.UUID `json:"recipient_user_id,omitempty"`
	Ciphertext      string     `json:"-"`
	WrappedDataKey  string     `json:"-"`
	ExpiresAt       *time.Time `json:"expires_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
}

func (e Envelope) Reference() Reference {
	return Reference{ID: e.ID, Purpose: e.Purpose, FieldPath: e.FieldPath}
}

// Context is deterministic authenticated context for both the per-record data
// key and ciphertext. Moving ciphertext between records or fields therefore
// fails decryption.
func (e Envelope) Context() ([]byte, error) {
	return json.Marshal(struct {
		ID              uuid.UUID  `json:"id"`
		Purpose         KeyPurpose `json:"purpose"`
		KeyVersion      int32      `json:"key_version"`
		SignalType      string     `json:"signal_type"`
		FieldPath       string     `json:"field_path"`
		ScopeEventID    uuid.UUID  `json:"scope_event_id"`
		RecipientUserID *uuid.UUID `json:"recipient_user_id,omitempty"`
	}{
		ID: e.ID, Purpose: e.Purpose, KeyVersion: e.KeyVersion,
		SignalType: e.SignalType, FieldPath: e.FieldPath,
		ScopeEventID: e.ScopeEventID, RecipientUserID: e.RecipientUserID,
	})
}
