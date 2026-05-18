package evpanda

import (
	"context"
	"strings"
)

// RoamingIdentity is the OCPI roaming context for a message. PlatformID
// and PlatformName are required; TenantID and TenantName are optional but
// all-or-nothing (supply both or neither).
type RoamingIdentity struct {
	PlatformID   string
	PlatformName string
	TenantID     string
	TenantName   string
}

// ChargerIdentity is the OCPP charger context for a message. ChargerID is
// required; TenantID and TenantName are optional but all-or-nothing.
type ChargerIdentity struct {
	ChargerID  string
	TenantID   string
	TenantName string
}

func isNonEmpty(v string) bool {
	return strings.TrimSpace(v) != ""
}

// isTenantPairValid reports whether tenant ID and name are both set or
// both empty.
func isTenantPairValid(tenantID, tenantName string) bool {
	return isNonEmpty(tenantID) == isNonEmpty(tenantName)
}

func validateRoamingIdentity(id RoamingIdentity) bool {
	return isNonEmpty(id.PlatformID) &&
		isNonEmpty(id.PlatformName) &&
		isTenantPairValid(id.TenantID, id.TenantName)
}

func validateChargerIdentity(id ChargerIdentity) bool {
	return isNonEmpty(id.ChargerID) &&
		isTenantPairValid(id.TenantID, id.TenantName)
}

// contextKey is the unexported type for this package's context keys, so
// they can't collide with keys from other packages.
type contextKey int

const (
	roamingIdentityKey contextKey = iota
	chargerIdentityKey
)

// WithRoamingIdentity returns a copy of ctx carrying the OCPI roaming
// identity, retrievable with RoamingIdentityFromContext.
func WithRoamingIdentity(ctx context.Context, id RoamingIdentity) context.Context {
	return context.WithValue(ctx, roamingIdentityKey, id)
}

// RoamingIdentityFromContext returns the OCPI roaming identity stored in
// ctx by WithRoamingIdentity. The bool is false if none is present.
func RoamingIdentityFromContext(ctx context.Context) (RoamingIdentity, bool) {
	id, ok := ctx.Value(roamingIdentityKey).(RoamingIdentity)
	return id, ok
}

// WithChargerIdentity returns a copy of ctx carrying the OCPP charger
// identity, retrievable with ChargerIdentityFromContext.
func WithChargerIdentity(ctx context.Context, id ChargerIdentity) context.Context {
	return context.WithValue(ctx, chargerIdentityKey, id)
}

// ChargerIdentityFromContext returns the OCPP charger identity stored in
// ctx by WithChargerIdentity. The bool is false if none is present.
func ChargerIdentityFromContext(ctx context.Context) (ChargerIdentity, bool) {
	id, ok := ctx.Value(chargerIdentityKey).(ChargerIdentity)
	return id, ok
}
