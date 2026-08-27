package ui

import (
	"context"
	"testing"

	"github.com/yogirk/tgcp/internal/core"
)

// TestServiceCommands_CoversEveryRegisteredService is the regression test
// for the bug where the command palette's service list was a hand-maintained
// duplicate (defaultCommands in internal/core/navigation.go) that silently
// stopped covering new services as they were registered over time — several
// services were completely unreachable via the ":" palette. serviceCommands
// must now produce exactly one command per registered service, with no
// hardcoded list to drift out of sync.
func TestServiceCommands_CoversEveryRegisteredService(t *testing.T) {
	cache := core.NewCache()
	registry := core.NewServiceRegistry(cache)
	registerAllServices(registry)
	svcMap := registry.InitializeAll(context.Background(), "")

	if len(svcMap) == 0 {
		t.Fatal("expected registerAllServices to register at least one service")
	}

	cmds := serviceCommands(svcMap)
	if len(cmds) != len(svcMap) {
		t.Fatalf("expected %d palette commands (one per registered service), got %d", len(svcMap), len(cmds))
	}

	seen := make(map[string]bool, len(cmds))
	for _, cmd := range cmds {
		route := cmd.Action()
		if route.View != core.ViewServiceList {
			t.Errorf("command %q: expected Action() to route to ViewServiceList, got %v", cmd.Name, route.View)
			continue
		}
		svc, ok := svcMap[route.Service]
		if !ok {
			t.Errorf("command %q routes to unknown service short name %q", cmd.Name, route.Service)
			continue
		}
		if cmd.Name != svc.Name() {
			t.Errorf("command for %q has Name %q, want %q", route.Service, cmd.Name, svc.Name())
		}
		seen[route.Service] = true
	}

	for shortName := range svcMap {
		if !seen[shortName] {
			t.Errorf("service %q has no palette command — it would be unreachable via ':'", shortName)
		}
	}
}
