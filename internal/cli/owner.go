package cli

import (
	"github.com/exterkamp/devrun/internal/herdr"
	"github.com/exterkamp/devrun/internal/registry"
)

// detectOwner records which herdr pane/agent ran `devrun up`. It returns nil
// outside a herdr pane; the server is then simply unowned.
func detectOwner() *registry.Owner {
	o := herdr.DetectOwner()
	if o == nil {
		return nil
	}
	return &registry.Owner{HerdrPane: o.Pane, HerdrWorkspace: o.Workspace, HerdrTab: o.Tab, Agent: o.Agent}
}
