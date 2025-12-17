package bot

import (
	"log"

	"phoenix-v3/internal/config"
	"phoenix-v3/internal/dexstate"
)

func InitDexStates(chains []config.ChainConfig) map[int64]*dexstate.UniV3State {
	states := make(map[int64]*dexstate.UniV3State)
	for _, ch := range chains {
		state, err := dexstate.NewUniV3State(ch.RPC)
		if err != nil {
			log.Printf("⚠️ Failed to connect RPC for chain %s: %v", ch.Name, err)
			continue
		}
		states[ch.ID] = state
		log.Printf("✅ Connected to RPC %s (chain %d)", ch.Name, ch.ID)
	}
	return states
}
