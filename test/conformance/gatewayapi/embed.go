// SPDX-License-Identifier: AGPL-3.0-only
//go:build conformance

package gatewayapi

import "embed"

// Manifests contains the project-specific base resources used by the Gateway
// API conformance suite. The base file mirrors the upstream manifests but is
// tailored to satisfy Network Services Operator validation constraints.
//
//go:embed base/*
var Manifests embed.FS
