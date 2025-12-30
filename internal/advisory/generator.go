package advisory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"phoenix-v3/internal/riskcontrol"
)

// Generator periodically generates risk advisories and writes them to disk.
// Phase 6.0: This is READ-ONLY. It NEVER writes to control.json.
type Generator struct {
	cfg        AdvisoryConfig

	// Phase 6.3: State manager for hysteresis
	stateManager *AdvisoryStateManager
