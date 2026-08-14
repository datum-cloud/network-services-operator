// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
)

func decodeIntoErr(data []byte, into runtime.Object) error {
	scheme := runtime.NewScheme()
	if err := AddToScheme(scheme); err != nil {
		return err
	}
	if err := RegisterDefaults(scheme); err != nil {
		return err
	}

	codecs := serializer.NewCodecFactory(scheme, serializer.EnableStrict)
	return runtime.DecodeInto(codecs.UniversalDecoder(), data, into)
}

func decodeInto(t *testing.T, data []byte, into runtime.Object) {
	t.Helper()
	if err := decodeIntoErr(data, into); err != nil {
		t.Fatalf("unable to decode config: %v", err)
	}
}

func validCell() CellControllerManager {
	cfg := CellControllerManager{}
	cfg.Location = LocationConfig{Name: "us-central-1"}
	cfg.IPAM.KubeconfigPath = "/etc/ipam-cluster/kubeconfig"
	return cfg
}

func TestCellControllerManager_Validate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*CellControllerManager)
		wantSub string
	}{
		{
			name:   "valid",
			mutate: func(*CellControllerManager) {},
		},
		{
			name:   "no configured location",
			mutate: func(c *CellControllerManager) { c.Location = LocationConfig{} },
		},
		{
			name:    "no ipam connection",
			mutate:  func(c *CellControllerManager) { c.IPAM.KubeconfigPath = "" },
			wantSub: "one of kubeconfigPath or inCluster is required",
		},
		{
			name: "conflicting ipam connection",
			mutate: func(c *CellControllerManager) {
				c.IPAM.InCluster = true
			},
			wantSub: "mutually exclusive",
		},
		{
			name: "in-cluster ipam",
			mutate: func(c *CellControllerManager) {
				c.IPAM.KubeconfigPath = ""
				c.IPAM.InCluster = true
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validCell()
			tt.mutate(&cfg)

			err := cfg.Validate()
			if tt.wantSub == "" {
				if err != nil {
					t.Fatalf("expected nil, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected an error containing %q, got nil", tt.wantSub)
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("expected an error containing %q, got %q", tt.wantSub, err.Error())
			}
		})
	}
}

func TestDecodeCellControllerManager(t *testing.T) {
	data := []byte(`apiVersion: apiserver.config.datumapis.com/v1alpha1
kind: CellControllerManager
ipam:
  kubeconfigPath: /etc/ipam-cluster/kubeconfig
location:
  name: us-central-1
`)

	var cfg CellControllerManager
	decodeInto(t, data, &cfg)

	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected the config to validate, got %v", err)
	}
	if cfg.Location.Name != "us-central-1" {
		t.Errorf("expected the location to decode, got %q", cfg.Location.Name)
	}
}

func TestDecodeCellControllerManager_RejectsControlPlaneFields(t *testing.T) {
	data := []byte(`apiVersion: apiserver.config.datumapis.com/v1alpha1
kind: CellControllerManager
gateway:
  targetDomain: example.com
location:
  name: us-central-1
`)

	var cfg CellControllerManager
	if err := decodeIntoErr(data, &cfg); err == nil {
		t.Fatal("expected strict decoding to reject a control-plane field, got nil")
	}
}
