package ui

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/yogirk/tgcp/internal/config"
	"github.com/yogirk/tgcp/internal/core"
	"github.com/yogirk/tgcp/internal/demo"
)

// TestServiceCommands_CoversEveryRegisteredService is the regression test
// for the bug where the command palette's service list was a hand-maintained
// duplicate (defaultCommands in internal/core/navigation.go) that silently
// stopped covering new services as they were registered over time — several
// services were completely unreachable via the ":" palette. serviceCommands
// must produce at least one command per registered service (services with
// extra sub-tabs, see serviceSubTabs, get more than one), with exactly one
// of them being the service's own top-level entry (SubTab == "").
func TestServiceCommands_CoversEveryRegisteredService(t *testing.T) {
	cache := core.NewCache()
	registry := core.NewServiceRegistry(cache)
	registerAllServices(registry)
	svcMap := registry.InitializeAll(context.Background(), "")

	if len(svcMap) == 0 {
		t.Fatal("expected registerAllServices to register at least one service")
	}

	cmds := serviceCommands(svcMap)
	if len(cmds) < len(svcMap) {
		t.Fatalf("expected at least %d palette commands (one per registered service), got %d", len(svcMap), len(cmds))
	}

	topLevelSeen := make(map[string]bool, len(svcMap))
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
		if route.SubTab == "" {
			if cmd.Name != svc.Name() {
				t.Errorf("top-level command for %q has Name %q, want %q", route.Service, cmd.Name, svc.Name())
			}
			if topLevelSeen[route.Service] {
				t.Errorf("service %q has more than one top-level (SubTab-less) command", route.Service)
			}
			topLevelSeen[route.Service] = true
		}
	}

	for shortName := range svcMap {
		if !topLevelSeen[shortName] {
			t.Errorf("service %q has no top-level palette command — it would be unreachable via ':'", shortName)
		}
	}
}

// TestServiceCommands_SubResourceAliasesAreSearchable is a regression test:
// searching the palette for "firewall" previously found nothing, because
// the only indexed command was named "VPC Network" ("firewall" appears
// nowhere in that service's own Name) and the substring-match tier only
// searched Name, never Description. Sub-tab commands (serviceSubTabs) now
// give each non-default tab its own independently-searchable/selectable
// entry with a matching Name, and selecting one must route with the right
// SubTab so the target service actually lands on that tab (see
// tabbedService/SetActiveTab), not just its default one.
func TestServiceCommands_SubResourceAliasesAreSearchable(t *testing.T) {
	cache := core.NewCache()
	registry := core.NewServiceRegistry(cache)
	registerAllServices(registry)
	svcMap := registry.InitializeAll(context.Background(), "")

	cmds := serviceCommands(svcMap)
	nav := core.NewNavigation()
	nav.SetBaseCommands(cmds)

	cases := []struct {
		query         string
		wantShortName string
		wantSubTab    string
	}{
		{"firewall", "net", "firewalls"},
		{"instance group", "gce", "instance-groups"},
		{"health check", "loadbalancing", "health-checks"},
		{"alert polic", "monitoring", "alert-policies"},
	}
	for _, c := range cases {
		nav.FilterCommands(c.query)
		if len(nav.Suggestions) == 0 {
			t.Errorf("query %q: expected at least one match, got none", c.query)
			continue
		}
		route := nav.Suggestions[0].Action()
		if route.Service != c.wantShortName || route.SubTab != c.wantSubTab {
			t.Errorf("query %q: expected top match to route to service=%q subTab=%q, got service=%q subTab=%q (%q)",
				c.query, c.wantShortName, c.wantSubTab, route.Service, route.SubTab, nav.Suggestions[0].Name)
		}
	}
}

// TestServiceSubTabs_ImplementTabbedServiceInterface verifies every service
// named in serviceSubTabs actually implements tabbedService and recognizes
// every key it advertises -- otherwise the SubTab in a sub-service command's
// Route would silently be a no-op (see the `if ts, ok := svc.(tabbedService)`
// type assertion in Update()) and selecting e.g. "VPC Network: Firewall
// Rules" would just land on the default tab with no error or indication.
func TestServiceSubTabs_ImplementTabbedServiceInterface(t *testing.T) {
	cache := core.NewCache()
	registry := core.NewServiceRegistry(cache)
	registerAllServices(registry)
	svcMap := registry.InitializeAll(context.Background(), "")

	for shortName, subs := range serviceSubTabs {
		svc, ok := svcMap[shortName]
		if !ok {
			t.Errorf("serviceSubTabs references unregistered service %q", shortName)
			continue
		}
		ts, ok := svc.(tabbedService)
		if !ok {
			t.Errorf("service %q is in serviceSubTabs but does not implement tabbedService (SetActiveTab)", shortName)
			continue
		}
		for _, sub := range subs {
			if ok, _ := ts.SetActiveTab(sub.Key); !ok {
				t.Errorf("service %q: SetActiveTab(%q) returned false (unrecognized key)", shortName, sub.Key)
			}
		}
	}
}

// TestPaletteServiceSelection_FocusLandsOnMain is a regression test for a
// real bug: selecting a service (or sub-service tab) from the command
// palette left keyboard focus wherever it was BEFORE the palette opened
// (m.LastFocus) instead of the newly-navigated-to service's own view --
// e.g. opening the palette from the landing page (FocusSidebar) and
// selecting a service via search closed the palette but silently reverted
// focus to FocusSidebar, so the service's table never received up/down/etc.
// at all ("frozen, cannot select next row"). Fixed by not clobbering the
// FocusMain that ViewServiceList routing already sets.
func TestPaletteServiceSelection_FocusLandsOnMain(t *testing.T) {
	demo.Enabled = true
	t.Cleanup(func() { demo.Enabled = false })

	m := InitialModel(
		core.AuthState{Authenticated: true, ProjectID: "demo-project"},
		config.DefaultConfig(),
		core.VersionInfo{Version: "test"},
	)
	m.Width, m.Height = 120, 40
	m.Focus = FocusSidebar // simulate sitting on the landing page

	// Open the palette (captures LastFocus = FocusSidebar) and search for a
	// sub-service command.
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(":")})
	m = newModel.(MainModel)
	if !m.Navigation.PaletteActive {
		t.Fatal("expected palette to be active after ':'")
	}
	for _, r := range "health check" {
		newModel, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = newModel.(MainModel)
	}
	if len(m.Navigation.Suggestions) == 0 {
		t.Fatal("expected at least one suggestion for 'health check'")
	}

	newModel, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = newModel.(MainModel)

	if m.Navigation.PaletteActive {
		t.Fatal("expected palette to close after selecting a command")
	}
	if m.ViewMode != ViewService || m.ActiveService != "loadbalancing" {
		t.Fatalf("expected to land on the loadbalancing service, got ViewMode=%v ActiveService=%q", m.ViewMode, m.ActiveService)
	}
	if m.Focus != FocusMain {
		t.Errorf("expected Focus to land on FocusMain so the service's own view receives keypresses, got %v", m.Focus)
	}
}
