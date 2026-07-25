// Package redfish imports all component implementations related to the
// DMTF Redfish API (Dell iDRAC, HPE iLO, Lenovo XCC and other BMCs).
package redfish

import (
	// Bring in the internal plugin definitions.
	_ "github.com/warpstreamlabs/bento/internal/impl/redfish"
)
