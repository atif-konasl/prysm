// Package consensus contains types for block operation-specific events fired
// during the runtime of a beacon node such as attestations, voluntary
// exits, and slashing.
package consensus

import (
	"github.com/prysmaticlabs/ethereumapis/eth/v1alpha1"
)

// MinimalConsensusData is the data sent with MinimalConsensusInfo events.
type MinimalConsensusData struct {
	MinimalConsensusInfo *eth.MinimalConsensusInfo
}