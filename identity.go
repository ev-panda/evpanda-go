// Port of src/identity.ts. Per-message identity: shapes + validation. Two
// independent shapes (no shared base). validate* is the single rule;
// nothing here panics — absent/invalid ⇒ caller drops the message.
//
// The Node IdentityResolver / IdentityInput generics are part of the
// adapter (pull) path and are intentionally not ported.

package evpanda

import "strings"

// RoamingIdentity is the OCPI roaming context. platformId/platformName
// required; tenant is all-or-nothing.
type RoamingIdentity struct {
	PlatformID   string `json:"platformId"`
	PlatformName string `json:"platformName"`
	TenantID     string `json:"tenantId,omitempty"`
	TenantName   string `json:"tenantName,omitempty"`
}

// ChargerIdentity is the OCPP charger context. chargerId required; tenant
// is all-or-nothing.
type ChargerIdentity struct {
	ChargerID  string `json:"chargerId"`
	TenantID   string `json:"tenantId,omitempty"`
	TenantName string `json:"tenantName,omitempty"`
}

// isNonEmpty: present and not blank.
func isNonEmpty(v string) bool {
	return strings.TrimSpace(v) != ""
}

// isTenantPairValid: both tenantId & tenantName, or neither.
func isTenantPairValid(tenantID, tenantName string) bool {
	return isNonEmpty(tenantID) == isNonEmpty(tenantName)
}

// validateRoamingIdentity is true iff platformId + platformName are
// non-empty and tenant is all-or-nothing.
func validateRoamingIdentity(id RoamingIdentity) bool {
	return isNonEmpty(id.PlatformID) &&
		isNonEmpty(id.PlatformName) &&
		isTenantPairValid(id.TenantID, id.TenantName)
}

// validateChargerIdentity is true iff chargerId is non-empty and tenant is
// all-or-nothing.
func validateChargerIdentity(id ChargerIdentity) bool {
	return isNonEmpty(id.ChargerID) &&
		isTenantPairValid(id.TenantID, id.TenantName)
}
