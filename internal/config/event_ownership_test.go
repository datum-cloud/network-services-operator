package config

import (
	"strings"
	"testing"
)

const eventOwnershipConfigHeader = "apiVersion: apiserver.config.datumapis.com/v1alpha1\nkind: NetworkServicesOperator\n"

func TestEventOwnershipDefaultsToDisabled(t *testing.T) {
	cfg := &NetworkServicesOperator{}
	SetObjectDefaults_NetworkServicesOperator(cfg)
	if got := cfg.DownstreamResourceManagement.EventOwnership; got != EventOwnershipDisabled {
		t.Errorf("eventOwnership = %q, want %q", got, EventOwnershipDisabled)
	}

	var decoded NetworkServicesOperator
	decodeInto(t, []byte(eventOwnershipConfigHeader), &decoded)
	if got := decoded.DownstreamResourceManagement.EventOwnership; got != EventOwnershipDisabled {
		t.Errorf("decoded eventOwnership = %q, want %q", got, EventOwnershipDisabled)
	}
}

func TestEventOwnershipDecodesEachMode(t *testing.T) {
	for _, mode := range []EventOwnershipMode{EventOwnershipDisabled, EventOwnershipShadow, EventOwnershipEnforce} {
		t.Run(string(mode), func(t *testing.T) {
			var cfg NetworkServicesOperator
			decodeInto(t, []byte(eventOwnershipConfigHeader+"downstreamResourceManagement:\n  eventOwnership: "+string(mode)+"\n"), &cfg)
			if got := cfg.DownstreamResourceManagement.EventOwnership; got != mode {
				t.Errorf("eventOwnership = %q, want %q", got, mode)
			}
			if err := cfg.DownstreamResourceManagement.validate(); err != nil {
				t.Errorf("validate() = %v, want nil", err)
			}
		})
	}
}

func TestEventOwnershipRejectsUnknownMode(t *testing.T) {
	for _, value := range []string{"enforce", "Off", "Drop"} {
		t.Run(value, func(t *testing.T) {
			cfg := &NetworkServicesOperator{}
			SetObjectDefaults_NetworkServicesOperator(cfg)
			cfg.DownstreamResourceManagement.EventOwnership = EventOwnershipMode(value)
			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), "eventOwnership must be one of") {
				t.Errorf("Validate() = %v, want an eventOwnership error", err)
			}
		})
	}
}

func TestConfigDecodingIsStrict(t *testing.T) {
	var cfg NetworkServicesOperator
	err := decodeIntoErr([]byte(eventOwnershipConfigHeader+"downstreamResourceManagement:\n  notARealField: Shadow\n"), &cfg)
	if err == nil || !strings.Contains(err.Error(), "notARealField") {
		t.Errorf("decode error = %v, want an unknown-field error", err)
	}
}
