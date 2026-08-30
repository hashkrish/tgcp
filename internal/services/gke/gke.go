package gke

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/yogirk/tgcp/internal/core"
	"github.com/yogirk/tgcp/internal/ui/components"
)

// newClusterCreateForm builds the FormModel for creating a new GKE cluster.
func newClusterCreateForm() components.FormModel {
	return components.NewForm("Create Cluster", []components.FormField{
		{Label: "Name", Placeholder: "my-cluster", Required: true},
		{Label: "Zone/Location", Placeholder: "us-central1-a", Required: true},
		{Label: "Node Count", Default: "3", Required: true, Validate: func(v string) string {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n <= 0 {
				return "must be a positive number"
			}
			return ""
		}},
		{Label: "Machine Type", Default: "e2-medium", Required: true},
	})
}

const CacheTTL = 60 * time.Second

// -----------------------------------------------------------------------------
// Models & Msgs
// -----------------------------------------------------------------------------

// Tick message for background refresh
type tickMsg time.Time

// ViewState defines the current UI state of the service
type ViewState int

const (
	ViewList ViewState = iota
	ViewDetail
	ViewConfirmation
	ViewCreate
	ViewUpdate
	ViewMasterUpgrade
	ViewNodePoolUpgrade
)

// newNodePoolUpdateForm builds the FormModel for resizing a cluster's first
// node pool, seeded with its current node count. Multi-node-pool clusters
// only expose the first pool here.
func newNodePoolUpdateForm(cluster Cluster, pool NodePool) components.FormModel {
	return components.NewForm(fmt.Sprintf("Resize Node Pool: %s (%s)", pool.Name, cluster.Name), []components.FormField{
		{Label: "Node Count", Default: strconv.FormatInt(pool.InitialNodeCount, 10), Required: true, Validate: func(v string) string {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n < 0 {
				return "must be a non-negative integer"
			}
			return ""
		}},
	})
}

// newMasterUpgradeForm builds the FormModel for upgrading a cluster's
// control plane, matching `gcloud container clusters upgrade --master
// --cluster-version=VERSION`.
func newMasterUpgradeForm(cluster Cluster) components.FormModel {
	return components.NewForm("Upgrade Master: "+cluster.Name, []components.FormField{
		{Label: "Version", Default: "latest", Placeholder: "latest, 1.29, or 1.29.1-gke.100", Required: true},
	})
}

// newNodePoolUpgradeForm builds the FormModel for upgrading a cluster's
// first node pool's Kubernetes version, matching `gcloud container clusters
// upgrade --node-pool=POOL --cluster-version=VERSION`. Multi-node-pool
// clusters only expose the first pool here, matching newNodePoolUpdateForm's
// existing scope limit.
func newNodePoolUpgradeForm(cluster Cluster, pool NodePool) components.FormModel {
	return components.NewForm(fmt.Sprintf("Upgrade Node Pool: %s (%s)", pool.Name, cluster.Name), []components.FormField{
		{Label: "Version", Default: "latest", Placeholder: "latest, 1.29, or 1.29.1-gke.100", Required: true},
	})
}

// Msg types
type clustersMsg []Cluster
type errMsg error
type actionResultMsg struct {
	err error
	msg string
}
type k9sCredsMsg struct {
	context string
	err     error
}

// -----------------------------------------------------------------------------
// Service Definition
// -----------------------------------------------------------------------------

type Service struct {
	client    *Client
	projectID string
	table     *components.StandardTable

	// UI Components
	filter        components.FilterModel
	filterSession components.FilterSession[Cluster]
	spinner       components.SpinnerModel

	// State
	clusters []Cluster
	err      error

	// View State
	viewState       ViewState
	selectedCluster *Cluster

	// Confirmation State
	pendingAction string    // e.g. "connect"
	actionSource  ViewState // Where to return after confirmation

	// Job history bookkeeping: captured right before firing a mutating
	// command (once pendingAction/selectedCluster context may already be
	// cleared) so the generic actionResultMsg handler knows what to record.
	pendingJobResource string
	pendingJobName     string
	pendingJobAction   string

	// Create State
	createForm components.FormModel

	// Update State
	updateForm components.FormModel

	// Master/Node-Pool Upgrade State. pendingVersion is captured at
	// form-submit time so the confirmation dialog and the actual API call
	// use the same value regardless of what the form field holds later.
	upgradeForm    components.FormModel
	pendingVersion string

	// Cache
	cache *core.Cache
}

func NewService(cache *core.Cache) *Service {
	// 1. Table Setup
	columns := []table.Column{
		{Title: "Name", Width: 25},
		{Title: "Location", Width: 15},
		{Title: "Status", Width: 12},
		{Title: "Version", Width: 18},
		{Title: "Mode", Width: 10},
		{Title: "Nodes", Width: 8},
	}

	t := components.NewStandardTable(columns)

	svc := &Service{
		table:     t,
		filter:    components.NewFilterWithPlaceholder("Filter clusters..."),
		spinner:   components.NewSpinner(),
		viewState: ViewList,
		cache:     cache,
	}
	svc.filterSession = components.NewFilterSession(&svc.filter, svc.getFilteredClusters, svc.updateTable)
	return svc
}

func (s *Service) Name() string {
	return "Kubernetes Engine"
}

func (s *Service) ShortName() string {
	return "gke"
}

func (s *Service) HelpText() string {
	if s.viewState == ViewList {
		return "r:Refresh  /:Filter  K:k9s  l:Logs  Ent:Detail  n:Create"
	}
	if s.viewState == ViewDetail {
		return "Esc/q:Back  K:k9s  u:Update (resize node pool)  M:Upgrade Master  N:Upgrade Node Pool  d:Delete"
	}
	if s.viewState == ViewConfirmation {
		return "y:Confirm  n:Cancel"
	}
	if s.viewState == ViewCreate || s.viewState == ViewUpdate || s.viewState == ViewMasterUpgrade || s.viewState == ViewNodePoolUpgrade {
		return "Tab/↑↓:Move  Enter:Submit  Esc:Cancel"
	}
	return ""
}

// -----------------------------------------------------------------------------
// Lifecycle
// -----------------------------------------------------------------------------

func (s *Service) InitService(ctx context.Context, projectID string) error {
	s.projectID = projectID
	client, err := NewClient(ctx)
	if err != nil {
		return err
	}
	s.client = client
	return nil
}

// Reinit reinitializes the service with a new project ID
func (s *Service) Reinit(ctx context.Context, projectID string) error {
	s.Reset()
	return s.InitService(ctx, projectID)
}

func (s *Service) Init() tea.Cmd {
	return s.tick()
}

func (s *Service) tick() tea.Cmd {
	return tea.Tick(CacheTTL, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (s *Service) Refresh() tea.Cmd {
	return tea.Batch(
		s.spinner.Start(""),
		s.fetchClustersCmd(true),
	)
}

func (s *Service) Reset() {
	s.viewState = ViewList
	s.selectedCluster = nil
	s.pendingAction = ""
	s.pendingVersion = ""
	s.err = nil
	s.table.SetCursor(0)
	s.filter.ExitFilterMode()
}

func (s *Service) IsRootView() bool {
	return s.viewState == ViewList
}

func (s *Service) Focus() {
	s.table.Focus()
}

func (s *Service) Blur() {
	s.table.Blur()
}

// -----------------------------------------------------------------------------
// Update Loop
// -----------------------------------------------------------------------------

func (s *Service) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case components.SpinnerTickMsg:
		s.spinner, cmd = s.spinner.Update(msg)
		return s, cmd

	case tickMsg:
		return s, tea.Batch(s.fetchClustersCmd(false), s.tick())

	case clustersMsg:
		s.spinner.Stop()
		s.clusters = msg
		s.filterSession.Apply(s.clusters)
		return s, func() tea.Msg { return core.LastUpdatedMsg(time.Now()) }

	case errMsg:
		s.spinner.Stop()
		s.err = msg
		return s, nil

	case actionResultMsg:
		resource, name, action := s.pendingJobResource, s.pendingJobName, s.pendingJobAction
		s.pendingJobResource, s.pendingJobName, s.pendingJobAction = "", "", ""
		if msg.err != nil {
			if resource != "" {
				core.RecordJob(core.Job{
					ProjectID: s.projectID,
					Service:   s.ShortName(),
					Resource:  resource,
					Name:      name,
					Action:    action,
					Status:    core.JobFailed,
					Error:     msg.err.Error(),
				})
			}
			if s.viewState == ViewUpdate {
				s.updateForm.SubmitErr = msg.err.Error()
				return s, nil
			}
			s.err = msg.err
			return s, func() tea.Msg {
				return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
			}
		} else if msg.msg != "" {
			if resource != "" {
				core.RecordJob(core.Job{
					ProjectID: s.projectID,
					Service:   s.ShortName(),
					Resource:  resource,
					Name:      name,
					Action:    action,
					Status:    core.JobSuccess,
				})
			}
			return s, tea.Batch(
				func() tea.Msg {
					return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess}
				},
				s.Refresh(),
			)
		}
		return s, nil

	case k9sCredsMsg:
		if msg.err != nil {
			return s, func() tea.Msg {
				return actionResultMsg{err: fmt.Errorf("failed to get cluster credentials: %w", msg.err)}
			}
		}
		return s, tea.ExecProcess(exec.Command("k9s", "--context", msg.context), func(err error) tea.Msg {
			if err != nil {
				return actionResultMsg{err: err}
			}
			return actionResultMsg{msg: "k9s session ended"}
		})

	case tea.WindowSizeMsg:
		s.table.HandleWindowSizeDefault(msg)

	case tea.MouseMsg:
		// Forward mouse events to table for click selection
		if s.viewState == ViewList {
			var updatedTable *components.StandardTable
			updatedTable, cmd = s.table.Update(msg)
			s.table = updatedTable
			return s, cmd
		}

	case tea.KeyMsg:
		// Handle filter mode (only in list view)
		if s.viewState == ViewList {
			result := s.filterSession.HandleKey(msg)

			if result.Handled {
				if result.Cmd != nil {
					return s, result.Cmd
				}
				if !result.ShouldContinue {
					return s, nil
				}
				// Continue processing other keys
			}
		}

		// LIST VIEW
		if s.viewState == ViewList {
			switch msg.String() {
			case "r":
				return s, s.Refresh()
			case "enter":
				clusters := s.getFilteredClusters(s.clusters, s.filter.Value())
				if idx := s.table.Cursor(); idx >= 0 && idx < len(clusters) {
					s.selectedCluster = &clusters[idx]
					s.viewState = ViewDetail
				}
			case "K": // Launch k9s
				clusters := s.getFilteredClusters(s.clusters, s.filter.Value())
				if idx := s.table.Cursor(); idx >= 0 && idx < len(clusters) {
					c := clusters[idx]
					return s, s.launchK9s(c)
				}
			case "l": // Logs
				clusters := s.getFilteredClusters(s.clusters, s.filter.Value())
				if idx := s.table.Cursor(); idx >= 0 && idx < len(clusters) {
					c := clusters[idx]
					// Filter for GKE Cluster logs
					filter := fmt.Sprintf(`resource.type="k8s_cluster" AND resource.labels.cluster_name="%s" AND resource.labels.location="%s"`, c.Name, c.Location)
					heading := fmt.Sprintf("Cluster: %s", c.Name)
					return s, func() tea.Msg { return core.SwitchToLogsMsg{Filter: filter, Source: "gke", Heading: heading} }
				}
			case "n": // Create
				s.createForm = newClusterCreateForm()
				s.viewState = ViewCreate
				return s, nil
			}
			var updatedTable *components.StandardTable
			updatedTable, cmd = s.table.Update(msg)
			s.table = updatedTable
			return s, cmd
		}

		// DETAIL VIEW
		if s.viewState == ViewDetail {
			switch msg.String() {
			case "esc", "q":
				s.viewState = ViewList
				s.selectedCluster = nil
				return s, nil
			case "K": // Launch k9s
				if s.selectedCluster != nil {
					return s, s.launchK9s(*s.selectedCluster)
				}
			case "u": // Update (resize first node pool)
				if s.selectedCluster != nil && len(s.selectedCluster.NodePools) > 0 {
					s.updateForm = newNodePoolUpdateForm(*s.selectedCluster, s.selectedCluster.NodePools[0])
					s.viewState = ViewUpdate
				}
				return s, nil
			case "M": // Upgrade Master
				if s.selectedCluster != nil {
					s.upgradeForm = newMasterUpgradeForm(*s.selectedCluster)
					s.viewState = ViewMasterUpgrade
				}
				return s, nil
			case "N": // Upgrade first node pool
				if s.selectedCluster != nil && len(s.selectedCluster.NodePools) > 0 {
					s.upgradeForm = newNodePoolUpgradeForm(*s.selectedCluster, s.selectedCluster.NodePools[0])
					s.viewState = ViewNodePoolUpgrade
				}
				return s, nil
			case "d": // Delete (Confirm) — double-confirm, this is the most
				// destructive action in the app: it destroys every node pool
				// and workload in the cluster with no undo.
				if s.selectedCluster != nil {
					s.pendingAction = "delete"
					s.actionSource = ViewDetail
					s.viewState = ViewConfirmation
				}
				return s, nil
			}
		}

		// CONFIRMATION VIEW
		if s.viewState == ViewConfirmation {
			switch msg.String() {
			case "y", "enter":
				if s.pendingAction == "delete" {
					// First confirmation only escalates to a second one —
					// deleting a GKE cluster is irreversible and destroys
					// every node pool and workload in it.
					s.pendingAction = "delete-confirm2"
					return s, nil
				}
				var actionCmd tea.Cmd
				nextView := ViewList
				if s.pendingAction == "delete-confirm2" && s.selectedCluster != nil {
					s.pendingJobResource, s.pendingJobName, s.pendingJobAction = "cluster", s.selectedCluster.Name, "delete"
					actionCmd = s.DeleteClusterCmd(*s.selectedCluster)
					s.selectedCluster = nil
				} else if s.pendingAction == "master-upgrade" && s.selectedCluster != nil {
					s.pendingJobResource, s.pendingJobName, s.pendingJobAction = "cluster", s.selectedCluster.Name, "master-upgrade"
					actionCmd = s.UpgradeMasterCmd(*s.selectedCluster, s.pendingVersion)
					nextView = ViewDetail
				} else if s.pendingAction == "nodepool-upgrade" && s.selectedCluster != nil && len(s.selectedCluster.NodePools) > 0 {
					s.pendingJobResource, s.pendingJobName, s.pendingJobAction = "node pool", s.selectedCluster.NodePools[0].Name, "nodepool-upgrade"
					actionCmd = s.UpgradeNodePoolCmd(*s.selectedCluster, s.selectedCluster.NodePools[0], s.pendingVersion)
					nextView = ViewDetail
				}
				s.viewState = nextView
				s.pendingAction = ""
				s.pendingVersion = ""
				return s, actionCmd
			case "n", "esc", "q":
				s.viewState = s.actionSource
				s.pendingAction = ""
				return s, nil
			}
		}

		// CREATE VIEW
		if s.viewState == ViewCreate {
			result, fcmd := s.createForm.Update(msg)
			if result.Cancelled {
				s.viewState = ViewList
				return s, nil
			}
			if result.Submitted {
				vals := s.createForm.Values()
				nodeCount, _ := strconv.ParseInt(vals["Node Count"], 10, 64)
				s.viewState = ViewList
				s.pendingJobResource, s.pendingJobName, s.pendingJobAction = "cluster", vals["Name"], "create"
				return s, s.CreateClusterCmd(vals["Name"], vals["Zone/Location"], nodeCount, vals["Machine Type"])
			}
			return s, fcmd
		}

		// UPDATE VIEW
		if s.viewState == ViewUpdate {
			result, fcmd := s.updateForm.Update(msg)
			if result.Cancelled {
				s.viewState = ViewDetail
				return s, nil
			}
			if result.Submitted && s.selectedCluster != nil && len(s.selectedCluster.NodePools) > 0 {
				nodeCount, _ := strconv.ParseInt(s.updateForm.Value("Node Count"), 10, 64)
				cluster := *s.selectedCluster
				pool := cluster.NodePools[0]
				s.viewState = ViewDetail
				s.pendingJobResource, s.pendingJobName, s.pendingJobAction = "node pool", pool.Name, "resize"
				return s, s.ResizeNodePoolCmd(cluster, pool, nodeCount)
			}
			return s, fcmd
		}

		// MASTER UPGRADE VIEW
		if s.viewState == ViewMasterUpgrade {
			result, fcmd := s.upgradeForm.Update(msg)
			if result.Cancelled {
				s.viewState = ViewDetail
				return s, nil
			}
			if result.Submitted && s.selectedCluster != nil {
				s.pendingVersion = s.upgradeForm.Value("Version")
				s.pendingAction = "master-upgrade"
				s.actionSource = ViewDetail
				s.viewState = ViewConfirmation
				return s, nil
			}
			return s, fcmd
		}

		// NODE POOL UPGRADE VIEW
		if s.viewState == ViewNodePoolUpgrade {
			result, fcmd := s.upgradeForm.Update(msg)
			if result.Cancelled {
				s.viewState = ViewDetail
				return s, nil
			}
			if result.Submitted && s.selectedCluster != nil {
				s.pendingVersion = s.upgradeForm.Value("Version")
				s.pendingAction = "nodepool-upgrade"
				s.actionSource = ViewDetail
				s.viewState = ViewConfirmation
				return s, nil
			}
			return s, fcmd
		}
	}

	return s, nil
}

// -----------------------------------------------------------------------------
// View Rendering
// -----------------------------------------------------------------------------

func (s *Service) View() string {
	if s.err != nil {
		return components.RenderError(s.err, s.Name(), "Clusters")
	}

	// Show animated spinner while loading
	if s.spinner.IsActive() {
		return s.spinner.View()
	}

	if s.viewState == ViewDetail {
		return s.renderDetailView()
	}

	if s.viewState == ViewConfirmation {
		return s.renderConfirmation()
	}

	if s.viewState == ViewCreate {
		return s.createForm.View()
	}

	if s.viewState == ViewUpdate {
		return s.updateForm.View()
	}

	if s.viewState == ViewMasterUpgrade || s.viewState == ViewNodePoolUpgrade {
		return s.upgradeForm.View()
	}

	return s.renderListView()
}

func (s *Service) renderListView() string {
	// Filter Bar
	var content strings.Builder
	content.WriteString(components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Clusters",
	))
	content.WriteString("\n")
	content.WriteString(s.filter.View())
	content.WriteString("\n")

	if len(s.clusters) == 0 {
		content.WriteString(components.EmptyState("clusters"))
		return content.String()
	}

	content.WriteString(s.table.View())
	return content.String()
}

// -----------------------------------------------------------------------------
// Helper Commands
// -----------------------------------------------------------------------------

func (s *Service) fetchClustersCmd(force bool) tea.Cmd {
	return func() tea.Msg {
		key := fmt.Sprintf("gke_clusters:%s", s.projectID)

		if !force && s.cache != nil {
			if val, found := s.cache.Get(key); found {
				if items, ok := val.([]Cluster); ok {
					return clustersMsg(items)
				}
			}
		}

		if s.client == nil {
			return errMsg(fmt.Errorf("client not initialized"))
		}

		clusters, err := s.client.ListClusters(s.projectID)
		if err != nil {
			return errMsg(err)
		}

		if s.cache != nil {
			s.cache.Set(key, clusters, CacheTTL)
		}

		return clustersMsg(clusters)
	}
}

// CreateClusterCmd triggers creation of a new GKE cluster
func (s *Service) CreateClusterCmd(name, location string, nodeCount int64, machineType string) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.CreateCluster(s.projectID, location, name, nodeCount, machineType); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Creating cluster %s...", name)}
	}
}

// DeleteClusterCmd triggers deletion of the given GKE cluster
func (s *Service) DeleteClusterCmd(cluster Cluster) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.DeleteCluster(s.projectID, cluster.Location, cluster.Name); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Deleting cluster %s...", cluster.Name)}
	}
}

// ResizeNodePoolCmd triggers a node-pool resize for the given cluster/pool
func (s *Service) ResizeNodePoolCmd(cluster Cluster, pool NodePool, nodeCount int64) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.ResizeNodePool(s.projectID, cluster.Location, cluster.Name, pool.Name, nodeCount); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Resizing node pool %s to %d nodes...", pool.Name, nodeCount)}
	}
}

// UpgradeMasterCmd triggers a control-plane version upgrade for the given cluster.
func (s *Service) UpgradeMasterCmd(cluster Cluster, version string) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.UpgradeMaster(s.projectID, cluster.Location, cluster.Name, version); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Upgrading master for cluster %s to %s...", cluster.Name, version)}
	}
}

// UpgradeNodePoolCmd triggers a version upgrade for the given cluster/pool.
func (s *Service) UpgradeNodePoolCmd(cluster Cluster, pool NodePool, version string) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.UpgradeNodePool(s.projectID, cluster.Location, cluster.Name, pool.Name, version); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Upgrading node pool %s to %s...", pool.Name, version)}
	}
}

func (s *Service) updateTable(items []Cluster) {
	rows := make([]table.Row, len(items))
	for i, item := range items {
		status := item.Status
		if item.Status == "RUNNING" {
			status = "RUNNING" // could add color here but table handles it poorly
		}

		rows[i] = table.Row{
			item.Name,
			item.Location,
			status,
			item.MasterVersion,
			item.Mode,
			fmt.Sprintf("%d", item.NodeCount),
		}
	}
	s.table.SetRows(rows)
}

// getFilteredClusters returns filtered clusters based on the query string
func (s *Service) getFilteredClusters(clusters []Cluster, query string) []Cluster {
	if query == "" {
		return clusters
	}
	return components.FilterSlice(clusters, query, func(cluster Cluster, q string) bool {
		return components.ContainsMatch(cluster.Name, cluster.Location, cluster.Status, cluster.MasterVersion)(q)
	})
}

func (s *Service) launchK9s(c Cluster) tea.Cmd {
	contextName := fmt.Sprintf("gke_%s_%s_%s", s.projectID, c.Location, c.Name)
	return tea.ExecProcess(exec.Command("k9s", "--context", contextName), func(err error) tea.Msg {
		if err != nil {
			// Fallback: Try to get credentials first, then launch k9s.
			// The user might not have context set up.
			// A tea.ExecProcess callback must return a tea.Msg, so the retry is
			// dispatched as k9sCredsMsg and handled by Update, which can return
			// a real tea.Cmd (a second ExecProcess) — it cannot be nested here.
			credCmd := exec.Command("gcloud", "container", "clusters", "get-credentials", c.Name, "--zone", c.Location, "--project", s.projectID)
			credErr := credCmd.Run()
			return k9sCredsMsg{context: contextName, err: credErr}
		}
		return actionResultMsg{msg: "k9s session ended"}
	})
}
