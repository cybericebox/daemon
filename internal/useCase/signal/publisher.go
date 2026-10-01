package signalUseCase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/cybericebox/daemon/pkg/tools"

	secretModel "github.com/cybericebox/daemon/internal/model/secret"
	signalModel "github.com/cybericebox/daemon/internal/model/signal"
	secretUseCase "github.com/cybericebox/daemon/internal/useCase/secret"
)

// Outbox is deliberately small so a use case can use its transaction-bound
// repository without depending on PostgreSQL or sqlc types.
type Outbox interface {
	Create(context.Context, signalModel.Signal) error
}

// SecretStore seals plaintext values and returns only an opaque reference.
// The concrete store owns persistence and purpose-specific keys.
type SecretStore interface {
	Seal(context.Context, secretUseCase.SealInput) (secretModel.Reference, error)
}

type Publisher struct {
	outbox  Outbox
	secrets SecretStore
	now     func() time.Time
}

func NewPublisher(outbox Outbox, now func() time.Time) *Publisher {
	return &Publisher{outbox: outbox, now: now}
}

func NewPublisherWithSecrets(outbox Outbox, secrets SecretStore, now func() time.Time) *Publisher {
	return &Publisher{outbox: outbox, secrets: secrets, now: now}
}

func (p *Publisher) Publish(ctx context.Context, typ signalModel.Type, payload signalModel.Payload) error {
	persisted, err := p.sealPayloadSecrets(ctx, typ, payload)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(persisted)
	if err != nil {
		return fmt.Errorf("signal: marshal %s: %w", typ, err)
	}
	now := p.now()
	return p.outbox.Create(ctx, signalModel.Signal{
		ID:         tools.NewUUIDv7(),
		Type:       typ,
		OccurredAt: now,
		Payload:    raw,
	})
}

var secretValueType = reflect.TypeOf(secretModel.Value{})

func (p *Publisher) sealPayloadSecrets(ctx context.Context, typ signalModel.Type, payload signalModel.Payload) (signalModel.Payload, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("signal: clone %s payload: %w", typ, err)
	}
	value := reflect.ValueOf(payload)
	if !value.IsValid() {
		return nil, errors.New("signal: nil payload")
	}
	var clone reflect.Value
	if value.Kind() == reflect.Ptr {
		if value.IsNil() {
			return nil, errors.New("signal: nil payload")
		}
		clone = reflect.New(value.Elem().Type())
		if err := json.Unmarshal(raw, clone.Interface()); err != nil {
			return nil, fmt.Errorf("signal: clone %s payload: %w", typ, err)
		}
	} else {
		clonePtr := reflect.New(value.Type())
		if err := json.Unmarshal(raw, clonePtr.Interface()); err != nil {
			return nil, fmt.Errorf("signal: clone %s payload: %w", typ, err)
		}
		clone = clonePtr.Elem()
	}
	persisted, ok := clone.Interface().(signalModel.Payload)
	if !ok {
		return nil, fmt.Errorf("signal: cloned %s payload no longer implements signal payload", typ)
	}
	routing := persisted.Routing()
	if err := p.sealValue(ctx, typ, routing, clone, ""); err != nil {
		return nil, err
	}
	// For value payloads, Interface above captured a copy before reflection
	// replaced secret values. Read it again after mutation; pointer payloads
	// naturally share the same instance but follow the same safe path.
	persisted, ok = clone.Interface().(signalModel.Payload)
	if !ok {
		return nil, fmt.Errorf("signal: sealed %s payload no longer implements signal payload", typ)
	}
	return persisted, nil
}

func (p *Publisher) sealValue(ctx context.Context, typ signalModel.Type, routing signalModel.Routing, value reflect.Value, path string) error {
	if value.Kind() == reflect.Ptr {
		if value.IsNil() {
			return nil
		}
		return p.sealValue(ctx, typ, routing, value.Elem(), path)
	}
	if value.Kind() != reflect.Struct {
		return nil
	}
	typInfo := value.Type()
	for index := 0; index < value.NumField(); index++ {
		fieldInfo := typInfo.Field(index)
		if !fieldInfo.IsExported() {
			continue
		}
		field := value.Field(index)
		jsonName := strings.Split(fieldInfo.Tag.Get("json"), ",")[0]
		if jsonName == "-" {
			continue
		}
		if jsonName == "" {
			jsonName = fieldInfo.Name
		}
		fieldPath := jsonName
		if path != "" {
			fieldPath = path + "." + jsonName
		}
		if fieldInfo.Tag.Get("secret") == "true" {
			if field.Type() != secretValueType || !field.CanAddr() {
				return fmt.Errorf("signal: secret field %s must use secret.Value", fieldPath)
			}
			if p.secrets == nil {
				return fmt.Errorf("signal: secret store is required for %s", fieldPath)
			}
			secretValue := field.Addr().Interface().(*secretModel.Value)
			if secretValue.Reference() != nil {
				continue
			}
			ref, err := p.secrets.Seal(ctx, secretUseCase.SealInput{
				Purpose: secretModel.PurposeNotification, SignalType: string(typ), FieldPath: fieldPath,
				ScopeEventID: routing.ScopeEventID, RecipientUserID: routing.SubjectUserID,
				Plaintext: []byte(secretValue.Plaintext()),
			})
			if err != nil {
				return fmt.Errorf("signal: seal %s: %w", fieldPath, err)
			}
			secretValue.SetReference(ref)
			continue
		}
		if err := p.sealValue(ctx, typ, routing, field, fieldPath); err != nil {
			return err
		}
	}
	return nil
}
