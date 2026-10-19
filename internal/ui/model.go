package ui

import (
	"context"
	"fmt"
	"sort"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/yogirk/tgcp/internal/config"
	"github.com/yogirk/tgcp/internal/core"
	"github.com/yogirk/tgcp/internal/services"
	"github.com/yogirk/tgcp/internal/services/apis"
	"github.com/yogirk/tgcp/internal/services/appengine"
	"github.com/yogirk/tgcp/internal/services/artifactregistry"
	"github.com/yogirk/tgcp/internal/services/bigquery"
	"github.com/yogirk/tgcp/internal/services/bigtable"
	"github.com/yogirk/tgcp/internal/services/cloudbuild"
	"github.com/yogirk/tgcp/internal/services/cloudfunctions"
	"github.com/yogirk/tgcp/internal/services/cloudrun"
	"github.com/yogirk/tgcp/internal/services/cloudsql"
	"github.com/yogirk/tgcp/internal/services/cloudtasks"
	"github.com/yogirk/tgcp/internal/services/dataflow"
	"github.com/yogirk/tgcp/internal/services/dataproc"
	"github.com/yogirk/tgcp/internal/services/disks"
	"github.com/yogirk/tgcp/internal/services/dns"
	"github.com/yogirk/tgcp/internal/services/filestore"
	"github.com/yogirk/tgcp/internal/services/firestore"
	"github.com/yogirk/tgcp/internal/services/gce"
	"github.com/yogirk/tgcp/internal/services/gcs"
	"github.com/yogirk/tgcp/internal/services/gke"
	"github.com/yogirk/tgcp/internal/services/iam"
	"github.com/yogirk/tgcp/internal/services/ipaddress"
	"github.com/yogirk/tgcp/internal/services/jobs"
	"github.com/yogirk/tgcp/internal/services/kms"
	"github.com/yogirk/tgcp/internal/services/loadbalancing"
	"github.com/yogirk/tgcp/internal/services/logging"
	"github.com/yogirk/tgcp/internal/services/monitoring"
	"github.com/yogirk/tgcp/internal/services/net"
	"github.com/yogirk/tgcp/internal/services/overview"
	"github.com/yogirk/tgcp/internal/services/parametermanager"
	"github.com/yogirk/tgcp/internal/services/pubsub"
	"github.com/yogirk/tgcp/internal/services/redis"
	"github.com/yogirk/tgcp/internal/services/scheduler"
	"github.com/yogirk/tgcp/internal/services/secrets"
	"github.com/yogirk/tgcp/internal/services/spanner"
	"github.com/yogirk/tgcp/internal/ui/components"
)

// ViewMode defines the high-level view state
type ViewMode int

const (
	ViewHome ViewMode = iota
	ViewService
)

// FocusArea defines where the user input is directed
type FocusArea int

const (
	FocusSidebar FocusArea = iota
	FocusMain
	FocusPalette
)

// MainModel is the main application state
type MainModel struct {
	AuthState  core.AuthState
	Navigation core.NavigationModel

	// Layout
	Width  int
	Height int

	// Services
	ServiceMap map[string]services.Service
	CurrentSvc services.Service

	// Components
	Sidebar   components.SidebarModel
	HomeMenu  components.HomeMenuModel // Added
	StatusBar components.StatusBarModel
	Palette   components.PaletteModel // Added
	Spinner   components.SpinnerModel // Global loading spinner

	// statusMsgSetAt is when the current transient StatusBar.Message (from a
	// core.ToastMsg) was set. statusMsgExpiredMsg carries the timestamp it
	// should clear, so a message replaced before its own timer fires isn't
	// wiped out by the older timer.
	statusMsgSetAt time.Time

	// State
	ViewMode      ViewMode // Added
	Focus         FocusArea
	LastFocus     FocusArea
	ShowHelp      bool
	ActiveService string

	// External Managers
	ProjectManager  *core.ProjectManager
	ServiceRegistry *core.ServiceRegistry
	Cache           *core.Cache

	// Config is the loaded ~/.tgcprc, kept around (rather than only consulted
	// at startup) so runtime features like the configured-projects quick
	// switcher (Ctrl+g) can read it.
	Config *config.Config

	// Version Info
	Version    core.VersionInfo
	UpdateInfo *core.UpdateInfo // nil until checked

	// LandingStats is a lightweight resource-count snapshot shown on the
	// landing page. nil until the one-shot background fetch (see Init())
	// completes; deliberately never re-fetched on a timer -- staleness
	// until app restart is an acceptable tradeoff for a glanceable count,
	// not a live dashboard.
	LandingStats *overview.ResourceInventory
}

// InitialModel returns the initial state of the application
func InitialModel(authState core.AuthState, cfg *config.Config, version core.VersionInfo) MainModel {
	// Initialize Cache
	cache := core.NewCache()

	// Create service registry and register all services
	registry := core.NewServiceRegistry(cache)
	registerAllServices(registry)

	// Create service map but don't initialize services yet (lazy initialization)
	// Services will be initialized on first access
	svcMap := registry.InitializeAll(context.Background(), authState.ProjectID)

	// Initialize Components
	sb := components.NewSidebar()
	sb.Visible = cfg.UI.SidebarVisible
	statusBar := components.NewStatusBar()
	statusBar.SetFocusPane("HOME")

	nav := core.NewNavigation()
	nav.SetBaseCommands(append(nav.Commands, serviceCommands(svcMap)...))

	return MainModel{
		AuthState:       authState,
		Navigation:      nav,
		Sidebar:         sb,
		HomeMenu:        components.NewHomeMenu(),
		StatusBar:       statusBar,
		Palette:         components.NewPalette(),
		Spinner:         components.NewSpinner(),
		Focus:           FocusSidebar,
		ViewMode:        ViewHome,
		ServiceMap:      svcMap,
		ProjectManager:  core.NewProjectManager(cache),
		ServiceRegistry: registry,
		Cache:           cache,
		Config:          cfg,
		Version:         version,
	}
}

// Init initializes the bubbletea program
func (m MainModel) Init() tea.Cmd {
	// Start with mouse support and check for updates in background
	return tea.Batch(
		tea.EnableMouseCellMotion,
		core.CheckForUpdates(m.Version.Version),
		m.fetchLandingStatsCmd(),
	)
}

// landingStatsMsg carries the one-shot resource-count snapshot for the
// landing page's stats strip.
type landingStatsMsg overview.ResourceInventory

// statusMsgExpiredMsg reverts the status bar's message once a core.ToastMsg's
// display duration has elapsed. See statusMsgSetAt on MainModel.
type statusMsgExpiredMsg struct{ at time.Time }

// fetchLandingStatsCmd fetches a lightweight resource-count snapshot for the
// landing page's stats strip. Reuses the exact same cache key format the
// real Overview service uses for its own inventory fetch
// (internal/services/overview/overview.go fetchInventoryCmd) so opening the
// Overview tab later hits the same cache entry instead of double-fetching.
func (m MainModel) fetchLandingStatsCmd() tea.Cmd {
	return func() tea.Msg {
		cacheKey := fmt.Sprintf("billing:inventory:global:%s", m.AuthState.ProjectID)
		if m.Cache != nil {
			if val, found := m.Cache.Get(cacheKey); found {
				if inv, ok := val.(overview.ResourceInventory); ok {
					return landingStatsMsg(inv)
				}
			}
		}

		client, err := overview.NewClient(context.Background())
		if err != nil {
			return nil // best-effort; landing page just shows no stats strip
		}
		inv, err := client.GetGlobalInventory(m.AuthState.ProjectID)
		if err != nil {
			return nil
		}
		if m.Cache != nil {
			m.Cache.Set(cacheKey, inv, overview.CacheTTL)
		}
		return landingStatsMsg(inv)
	}
}

// getOrInitializeService gets a service from the map, initializing it lazily if needed
// This implements lazy initialization - services are only initialized when first accessed
func (m *MainModel) getOrInitializeService(ctx context.Context, serviceName string) (services.Service, error) {
	// First check if service exists in map
	if svc, exists := m.ServiceMap[serviceName]; exists {
		// Service exists, but may not be initialized yet
		// Use registry to ensure it's initialized
		if m.ServiceRegistry != nil {
			initializedSvc, err := m.ServiceRegistry.GetOrInitializeService(ctx, serviceName)
			if err != nil {
				return svc, err // Return original service if init fails
			}
			if initializedSvc != nil {
				// Update the map with the initialized service
				m.ServiceMap[serviceName] = initializedSvc
				return initializedSvc, nil
			}
		}
		return svc, nil
	}

	// Service doesn't exist in map - try to get it from registry (lazy creation)
	if m.ServiceRegistry != nil {
		svc, err := m.ServiceRegistry.GetOrInitializeService(ctx, serviceName)
		if err != nil {
			return nil, err
		}
		if svc != nil {
			// Add to map
			m.ServiceMap[serviceName] = svc
			return svc, nil
		}
	}

	return nil, nil // Service not found
}

// showConfiguredProjectSwitcher populates the palette with the projects
// listed under `projects:` in ~/.tgcprc, so the user can jump straight to one
// of their known projects without waiting on a Cloud Resource Manager API
// call. Selecting an entry routes through the same "SWITCH_PROJECT:<id>"
// mechanism as the API-backed switcher.
func (m *MainModel) showConfiguredProjectSwitcher() {
	var cmds []core.Command
	if m.Config != nil {
		for _, p := range m.Config.Projects {
			p := p // capture
			label := p.Name
			if label == "" {
				label = p.ID
			}
			cmds = append(cmds, core.Command{
				Name:        p.ID,
				Description: label,
				Action: func() core.Route {
					return core.Route{View: core.ViewHome, ID: "SWITCH_PROJECT:" + p.ID}
				},
			})
		}
	}

	if len(cmds) == 0 {
		m.StatusBar.Message = "No projects configured — add a `projects:` list to ~/.tgcprc"
		return
	}

	m.Navigation.SetCommands(cmds)
	m.StatusBar.Message = "Switch to configured project..."
}

// View renders the current UI based on state
// See home.go for the actual view logic

// registerAllServices registers all available services with the registry
// This is kept in the ui package to avoid import cycles (services import core, core shouldn't import services)
func registerAllServices(registry *core.ServiceRegistry) {
	registry.Register("overview", func(cache *core.Cache) services.Service {
		return overview.NewService(cache)
	})
	registry.Register("gce", func(cache *core.Cache) services.Service {
		return gce.NewService(cache)
	})
	registry.Register("gke", func(cache *core.Cache) services.Service {
		return gke.NewService(cache)
	})
	registry.Register("disks", func(cache *core.Cache) services.Service {
		return disks.NewService(cache)
	})
	registry.Register("filestore", func(cache *core.Cache) services.Service {
		return filestore.NewService(cache)
	})
	registry.Register("pubsub", func(cache *core.Cache) services.Service {
		return pubsub.NewService(cache)
	})
	registry.Register("scheduler", func(cache *core.Cache) services.Service {
		return scheduler.NewService(cache)
	})
	registry.Register("cloudtasks", func(cache *core.Cache) services.Service {
		return cloudtasks.NewService(cache)
	})
	registry.Register("redis", func(cache *core.Cache) services.Service {
		return redis.NewService(cache)
	})
	registry.Register("spanner", func(cache *core.Cache) services.Service {
		return spanner.NewService(cache)
	})
	registry.Register("bigtable", func(cache *core.Cache) services.Service {
		return bigtable.NewService(cache)
	})
	registry.Register("dataflow", func(cache *core.Cache) services.Service {
		return dataflow.NewService(cache)
	})
	registry.Register("dataproc", func(cache *core.Cache) services.Service {
		return dataproc.NewService(cache)
	})
	registry.Register("firestore", func(cache *core.Cache) services.Service {
		return firestore.NewService(cache)
	})
	registry.Register("sql", func(cache *core.Cache) services.Service {
		return cloudsql.NewService(cache)
	})
	registry.Register("iam", func(cache *core.Cache) services.Service {
		return iam.NewService(cache)
	})
	registry.Register("run", func(cache *core.Cache) services.Service {
		return cloudrun.NewService(cache)
	})
	registry.Register("appengine", func(cache *core.Cache) services.Service {
		return appengine.NewService(cache)
	})
	registry.Register("gcs", func(cache *core.Cache) services.Service {
		return gcs.NewService(cache)
	})
	registry.Register("bq", func(cache *core.Cache) services.Service {
		return bigquery.NewService(cache)
	})
	registry.Register("net", func(cache *core.Cache) services.Service {
		return net.NewService(cache)
	})
	registry.Register("ipaddress", func(cache *core.Cache) services.Service {
		return ipaddress.NewService(cache)
	})
	registry.Register("loadbalancing", func(cache *core.Cache) services.Service {
		return loadbalancing.NewService(cache)
	})
	registry.Register("dns", func(cache *core.Cache) services.Service {
		return dns.NewService(cache)
	})
	registry.Register("kms", func(cache *core.Cache) services.Service {
		return kms.NewService(cache)
	})
	registry.Register("functions", func(cache *core.Cache) services.Service {
		return cloudfunctions.NewService(cache)
	})
	registry.Register("logs", func(cache *core.Cache) services.Service {
		return logging.NewService(cache)
	})
	registry.Register("monitoring", func(cache *core.Cache) services.Service {
		return monitoring.NewService(cache)
	})
	registry.Register("secrets", func(cache *core.Cache) services.Service {
		return secrets.NewService(cache)
	})
	registry.Register("parametermanager", func(cache *core.Cache) services.Service {
		return parametermanager.NewService(cache)
	})
	registry.Register("cloudbuild", func(cache *core.Cache) services.Service {
		return cloudbuild.NewService(cache)
	})
	registry.Register("artifactregistry", func(cache *core.Cache) services.Service {
		return artifactregistry.NewService(cache)
	})
	registry.Register("apis", func(cache *core.Cache) services.Service {
		return apis.NewService(cache)
	})
	registry.Register("jobs", func(cache *core.Cache) services.Service {
		return jobs.NewService(cache)
	})
}

// tabbedService is implemented by services with more than one tab (see
// serviceSubTabs below). SetActiveTab returns false for an unrecognized key
// (callers treat that as a harmless no-op rather than an error, since a
// mistyped or stale key shouldn't break navigation), plus a tea.Cmd that
// MUST be run when non-nil -- switching to a tab whose data hasn't been
// fetched yet needs the same fetch the service's own '['/']' handling
// triggers (e.g. GCE's Instance Groups, Cloud Run's Functions, every
// Load Balancing tab past the default); forgetting this leaves the target
// tab's table permanently empty since nothing else will ever populate it.
type tabbedService interface {
	SetActiveTab(tab string) (bool, tea.Cmd)
}

// subServiceTab names one non-default tab of a tabbed service, so it can get
// its own independently-searchable/selectable palette command (e.g.
// "VPC Network: Firewall Rules") instead of only being reachable by first
// opening the service and then pressing '['/']'/Tab blind. Key is passed to
// the service's SetActiveTab; Label is appended to "<service name>: " to
// form the command's Name.
//
// Each service's own default tab (whatever Reset() sets activeTab to) is
// deliberately NOT listed here -- the service's own top-level command
// already opens on that tab, so adding a redundant "Foo: Default Tab" entry
// would just be a near-duplicate of "Foo" with no new capability.
type subServiceTab struct {
	Key   string
	Label string
}

// serviceSubTabs lists the extra (non-default) tabs per service short name.
// Keys here must match the case strings each service's own SetActiveTab
// switches on (internal/services/{gce,net,monitoring,loadbalancing,cloudrun,pubsub,cloudbuild}).
var serviceSubTabs = map[string][]subServiceTab{
	"gce":        {{Key: "instance-groups", Label: "Instance Groups (MIGs)"}},
	"net":        {{Key: "firewalls", Label: "Firewall Rules"}},
	"monitoring": {{Key: "alert-policies", Label: "Alert Policies"}},
	"loadbalancing": {
		{Key: "health-checks", Label: "Health Checks"},
		{Key: "url-maps", Label: "URL Maps"},
		{Key: "forwarding-rules", Label: "Forwarding Rules"},
		{Key: "ssl-certificates", Label: "SSL Certificates"},
	},
	"run":    {{Key: "functions", Label: "Functions"}},
	"pubsub": {{Key: "subscriptions", Label: "Subscriptions"}},
	"cloudbuild": {
		{Key: "triggers", Label: "Triggers"},
		{Key: "worker-pools", Label: "Worker Pools"},
		{Key: "connections", Label: "Connections"},
	},
}

// serviceCommands builds one command-palette entry per registered service,
// derived directly from the service registry rather than a hand-maintained
// list — a hardcoded list previously drifted out of sync as new services
// were added, silently leaving them unreachable from the ":" palette. Sorted
// alphabetically by display name for a stable, predictable order.
func serviceCommands(svcMap map[string]services.Service) []core.Command {
	cmds := make([]core.Command, 0, len(svcMap))
	for shortName, svc := range svcMap {
		name := svc.Name()
		short := shortName
		cmds = append(cmds, core.Command{
			Name:        name,
			Description: fmt.Sprintf("Open %s (%s)", name, short),
			Action: func() core.Route {
				return core.Route{View: core.ViewServiceList, Service: short}
			},
		})

		for _, sub := range serviceSubTabs[short] {
			sub := sub
			cmds = append(cmds, core.Command{
				Name:        fmt.Sprintf("%s: %s", name, sub.Label),
				Description: fmt.Sprintf("Open %s under %s (%s)", sub.Label, name, short),
				Action: func() core.Route {
					return core.Route{View: core.ViewServiceList, Service: short, SubTab: sub.Key}
				},
			})
		}
	}
	sort.Slice(cmds, func(i, j int) bool { return cmds[i].Name < cmds[j].Name })
	return cmds
}

func (m *MainModel) setFocus(area FocusArea) {
	m.Focus = area
	if m.ViewMode == ViewService && m.CurrentSvc != nil {
		switch area {
		case FocusMain:
			m.CurrentSvc.Focus()
		case FocusSidebar:
			m.CurrentSvc.Blur()
		}
	}
	m.syncStatusBarFocus()
}

func (m *MainModel) syncStatusBarFocus() {
	if m.ViewMode == ViewHome {
		m.StatusBar.SetFocusPane("HOME")
		return
	}

	switch m.Focus {
	case FocusSidebar:
		m.StatusBar.SetFocusPane("SIDEBAR")
	case FocusMain:
		m.StatusBar.SetFocusPane("MAIN")
	default:
		m.StatusBar.SetFocusPane("")
	}
}
