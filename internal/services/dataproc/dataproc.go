package dataproc

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/yogirk/tgcp/internal/core"
	"github.com/yogirk/tgcp/internal/ui/components"
)

const CacheTTL = 60 * time.Second
const DefaultRegion = "us-central1" // Simplification for MVP

// -----------------------------------------------------------------------------
// Models
// -----------------------------------------------------------------------------

type tickMsg time.Time

type ViewState int

const (
	ViewList ViewState = iota
	ViewDetail
	ViewCreate
	ViewUpdate
	ViewConfirmation
	ViewIAM
	ViewIAMForm
	ViewJobs
	ViewJobSubmit
)

// newClusterUpdateForm builds the FormModel for resizing a cluster's
// primary worker group, seeded with its current node count. Secondary
// workers, autoscaling policies, and graceful decommission are out of
// scope.
func newClusterUpdateForm(cluster Cluster) components.FormModel {
	return components.NewForm("Update Cluster: "+cluster.Name, []components.FormField{
		{Label: "Num Workers", Default: strconv.Itoa(cluster.WorkerCount), Required: true, Validate: func(v string) string {
			n, err := strconv.Atoi(v)
			if err != nil || n <= 0 {
				return "must be a positive integer"
			}
			return ""
		}},
	})
}

// newSparkJobForm builds the FormModel for submitting a minimal Spark job.
func newSparkJobForm(cluster Cluster) components.FormModel {
	return components.NewForm("Submit Spark Job: "+cluster.Name, []components.FormField{
		{Label: "Main Class", Placeholder: "org.apache.spark.examples.SparkPi", Required: true},
		{Label: "Jar URI", Placeholder: "file:///usr/lib/spark/examples/jars/spark-examples.jar", Required: true},
	})
}

type clustersMsg []Cluster
type errMsg error

// actionResultMsg carries the result of an async action (e.g. cluster creation)
type actionResultMsg struct {
	err error
	msg string
}

// iamPolicyMsg carries the result of a GetClusterIAMPolicy fetch.
type iamPolicyMsg struct {
	bindings []IAMBinding
	err      error
}

// jobsMsg carries the result of a ListJobs fetch.
type jobsMsg struct {
	jobs []JobInfo
	err  error
}

// -----------------------------------------------------------------------------
// Service Definition
// -----------------------------------------------------------------------------

type Service struct {
	client    *Client
	projectID string
	table     *components.StandardTable

	filter        components.FilterModel
	filterSession components.FilterSession[Cluster]

	clusters []Cluster
	spinner  components.SpinnerModel
	err      error

	viewState       ViewState
	selectedCluster *Cluster

	createForm components.FormModel
	updateForm components.FormModel

	// Confirmation State
	pendingAction string    // "delete", "start", "stop", "diagnose", "grant"
	actionSource  ViewState // Where to return after confirmation

	// IAM State: current bindings for the selected cluster, and the
	// add-binding form.
	iamBindings      []IAMBinding
	iamForm          components.FormModel
	pendingIAMRole   string
	pendingIAMMember string

	// Jobs State
	jobs          []JobInfo
	jobTable      *components.StandardTable
	jobSubmitForm components.FormModel
	selectedJob   *JobInfo

	cache *core.Cache
}

func NewService(cache *core.Cache) *Service {
	columns := []table.Column{
		{Title: "Cluster Name", Width: 30},
		{Title: "Status", Width: 15},
		{Title: "Workers", Width: 10},
		{Title: "Zone", Width: 15},
	}

	t := components.NewStandardTable(columns)

	jobColumns := []table.Column{
		{Title: "Job ID", Width: 30},
		{Title: "Type", Width: 12},
		{Title: "State", Width: 15},
	}
	jobTable := components.NewStandardTable(jobColumns)

	svc := &Service{
		table:     t,
		jobTable:  jobTable,
		filter:    components.NewFilterWithPlaceholder("Filter clusters..."),
		spinner:   components.NewSpinner(),
		viewState: ViewList,
		cache:     cache,
	}
	svc.filterSession = components.NewFilterSession(&svc.filter, svc.getFilteredClusters, svc.updateTable)
	return svc
}

func (s *Service) Name() string {
	return "Dataproc"
}

func (s *Service) ShortName() string {
	return "dataproc"
}

func (s *Service) HelpText() string {
	if s.viewState == ViewList {
		return "r:Refresh  /:Filter  Ent:Detail  n:New Cluster"
	}
	if s.viewState == ViewCreate || s.viewState == ViewUpdate || s.viewState == ViewIAMForm || s.viewState == ViewJobSubmit {
		return "Tab/↑↓:Move  Enter/Ctrl+S:Submit  Esc:Cancel"
	}
	if s.viewState == ViewDetail {
		return "Esc/q:Back  u:Update  s:Start  x:Stop  g:Diagnose  i:IAM  J:Jobs  d:Delete"
	}
	if s.viewState == ViewIAM {
		return "Esc/q:Back  a:Grant Role"
	}
	if s.viewState == ViewJobs {
		return "Esc/q:Back  r:Refresh  n:Submit Spark Job  k:Kill"
	}
	if s.viewState == ViewConfirmation {
		return "y:Confirm  n:Cancel"
	}
	return "Esc/q:Back"
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
	s.selectedJob = nil
	s.pendingAction = ""
	s.iamBindings = nil
	s.jobs = nil
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
// Update
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
		if s.selectedCluster != nil {
			for i := range s.clusters {
				if s.clusters[i].Name == s.selectedCluster.Name {
					s.selectedCluster = &s.clusters[i]
					break
				}
			}
		}
		return s, func() tea.Msg { return core.LastUpdatedMsg(time.Now()) }

	case errMsg:
		s.spinner.Stop()
		s.err = msg
		return s, nil

	case iamPolicyMsg:
		s.spinner.Stop()
		if msg.err != nil {
			return s, func() tea.Msg {
				return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
			}
		}
		s.iamBindings = msg.bindings
		s.viewState = ViewIAM
		return s, nil

	case jobsMsg:
		s.spinner.Stop()
		if msg.err != nil {
			return s, func() tea.Msg {
				return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
			}
		}
		s.jobs = msg.jobs
		s.updateJobTable(s.jobs)
		s.viewState = ViewJobs
		return s, nil

	case actionResultMsg:
		if s.pendingAction == "grant" {
			s.pendingAction = ""
			if msg.err != nil {
				return s, func() tea.Msg {
					return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
				}
			}
			if s.selectedCluster != nil {
				return s, tea.Batch(
					func() tea.Msg { return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess} },
					s.fetchIAMCmd(*s.selectedCluster),
				)
			}
			return s, func() tea.Msg { return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess} }
		}
		if s.pendingAction == "kill-job" {
			s.pendingAction = ""
			s.selectedJob = nil
			if msg.err != nil {
				return s, func() tea.Msg {
					return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
				}
			}
			return s, tea.Batch(
				func() tea.Msg { return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess} },
				s.fetchJobsCmd(),
			)
		}
		if s.pendingAction == "diagnose" {
			s.pendingAction = ""
			if msg.err != nil {
				return s, func() tea.Msg {
					return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
				}
			}
			return s, func() tea.Msg { return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess} }
		}
		if s.viewState == ViewJobSubmit {
			if msg.err != nil {
				s.jobSubmitForm.SubmitErr = msg.err.Error()
				return s, nil
			}
			return s, tea.Batch(
				func() tea.Msg { return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess} },
				s.fetchJobsCmd(),
			)
		}
		if s.pendingAction == "delete" || s.pendingAction == "start" || s.pendingAction == "stop" {
			action := s.pendingAction
			s.pendingAction = ""
			if msg.err != nil {
				return s, func() tea.Msg {
					return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
				}
			}
			if action == "delete" {
				s.selectedCluster = nil
				s.viewState = ViewList
			}
			return s, tea.Batch(
				func() tea.Msg {
					return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess}
				},
				s.Refresh(),
			)
		}
		if msg.err != nil {
			if s.viewState == ViewUpdate {
				s.updateForm.SubmitErr = msg.err.Error()
			} else {
				s.createForm.SubmitErr = msg.err.Error()
			}
			return s, nil
		}
		s.viewState = ViewList
		return s, tea.Batch(
			func() tea.Msg {
				return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess}
			},
			s.Refresh(),
		)

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
		if s.viewState == ViewCreate {
			result, formCmd := s.createForm.Update(msg)
			if result.Cancelled {
				s.viewState = ViewList
				return s, nil
			}
			if result.Submitted {
				return s, s.createClusterCmd()
			}
			return s, formCmd
		}

		if s.viewState == ViewUpdate {
			result, formCmd := s.updateForm.Update(msg)
			if result.Cancelled {
				s.viewState = ViewDetail
				return s, nil
			}
			if result.Submitted && s.selectedCluster != nil {
				return s, s.updateClusterCmd(*s.selectedCluster)
			}
			return s, formCmd
		}

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

		if s.viewState == ViewList {
			switch msg.String() {
			case "r":
				return s, s.Refresh()
			case "n":
				s.createForm = components.NewForm("Create Cluster", []components.FormField{
					{Label: "Cluster Name", Placeholder: "my-cluster", Required: true},
					{Label: "Region", Placeholder: DefaultRegion, Default: DefaultRegion, Required: true},
					{Label: "Zone", Placeholder: "us-central1-a"},
					{Label: "Master Machine Type", Placeholder: "n1-standard-2", Default: "n1-standard-2"},
					{Label: "Worker Machine Type", Placeholder: "n1-standard-2", Default: "n1-standard-2"},
					{Label: "Num Workers", Placeholder: "2", Default: "2"},
				})
				s.viewState = ViewCreate
				return s, nil
			case "enter":
				clusters := s.getFilteredClusters(s.clusters, s.filter.Value())
				if idx := s.table.Cursor(); idx >= 0 && idx < len(clusters) {
					s.selectedCluster = &clusters[idx]
					s.viewState = ViewDetail
				}
			}
			var updatedTable *components.StandardTable
			updatedTable, cmd = s.table.Update(msg)
			s.table = updatedTable
			return s, cmd
		}

		if s.viewState == ViewDetail {
			switch msg.String() {
			case "esc", "q":
				s.viewState = ViewList
				s.selectedCluster = nil
				return s, nil
			case "u":
				if s.selectedCluster != nil {
					s.updateForm = newClusterUpdateForm(*s.selectedCluster)
					s.viewState = ViewUpdate
				}
				return s, nil
			case "s": // Start (Confirm)
				if s.selectedCluster != nil {
					s.pendingAction = "start"
					s.actionSource = ViewDetail
					s.viewState = ViewConfirmation
				}
				return s, nil
			case "x": // Stop (Confirm)
				if s.selectedCluster != nil {
					s.pendingAction = "stop"
					s.actionSource = ViewDetail
					s.viewState = ViewConfirmation
				}
				return s, nil
			case "d": // Delete (Confirm)
				if s.selectedCluster != nil {
					s.pendingAction = "delete"
					s.actionSource = ViewDetail
					s.viewState = ViewConfirmation
				}
				return s, nil
			case "g": // Diagnose (Confirm)
				if s.selectedCluster != nil {
					s.pendingAction = "diagnose"
					s.actionSource = ViewDetail
					s.viewState = ViewConfirmation
				}
				return s, nil
			case "i": // IAM
				if s.selectedCluster != nil {
					return s, tea.Batch(s.fetchIAMCmd(*s.selectedCluster), s.spinner.Start(""))
				}
				return s, nil
			case "J": // Jobs
				if s.selectedCluster != nil {
					return s, tea.Batch(s.fetchJobsCmd(), s.spinner.Start(""))
				}
				return s, nil
			}
		}

		if s.viewState == ViewIAM {
			switch msg.String() {
			case "q", "esc":
				s.viewState = ViewDetail
				return s, nil
			case "a":
				if s.selectedCluster != nil {
					s.iamForm = components.NewIAMAddBindingForm(s.selectedCluster.Name)
					s.viewState = ViewIAMForm
				}
				return s, nil
			}
		}

		if s.viewState == ViewIAMForm {
			result, formCmd := s.iamForm.Update(msg)
			if result.Cancelled {
				s.viewState = ViewIAM
				return s, nil
			}
			if result.Submitted && s.selectedCluster != nil {
				s.pendingIAMRole = s.iamForm.Value("Role")
				s.pendingIAMMember = s.iamForm.Value("Member")
				s.pendingAction = "grant"
				s.actionSource = ViewIAM
				s.viewState = ViewConfirmation
				return s, nil
			}
			return s, formCmd
		}

		if s.viewState == ViewJobs {
			switch msg.String() {
			case "q", "esc":
				s.viewState = ViewDetail
				return s, nil
			case "r":
				return s, tea.Batch(s.fetchJobsCmd(), s.spinner.Start(""))
			case "n": // Submit a new Spark job
				if s.selectedCluster != nil {
					s.jobSubmitForm = newSparkJobForm(*s.selectedCluster)
					s.viewState = ViewJobSubmit
				}
				return s, nil
			case "k": // Kill selected job (Confirm)
				if idx := s.jobTable.Cursor(); idx >= 0 && idx < len(s.jobs) {
					s.selectedJob = &s.jobs[idx]
					s.pendingAction = "kill-job"
					s.actionSource = ViewJobs
					s.viewState = ViewConfirmation
				}
				return s, nil
			}
			var updatedJobTable *components.StandardTable
			updatedJobTable, cmd = s.jobTable.Update(msg)
			s.jobTable = updatedJobTable
			return s, cmd
		}

		if s.viewState == ViewJobSubmit {
			result, formCmd := s.jobSubmitForm.Update(msg)
			if result.Cancelled {
				s.viewState = ViewJobs
				return s, nil
			}
			if result.Submitted && s.selectedCluster != nil {
				return s, s.submitSparkJobCmd(*s.selectedCluster, s.jobSubmitForm.Value("Main Class"), s.jobSubmitForm.Value("Jar URI"))
			}
			return s, formCmd
		}

		if s.viewState == ViewConfirmation {
			switch msg.String() {
			case "y", "enter":
				var actionCmd tea.Cmd
				if s.selectedCluster != nil {
					switch s.pendingAction {
					case "delete":
						actionCmd = s.deleteClusterCmd(*s.selectedCluster)
					case "start":
						actionCmd = s.startClusterCmd(*s.selectedCluster)
					case "stop":
						actionCmd = s.stopClusterCmd(*s.selectedCluster)
					case "diagnose":
						actionCmd = s.diagnoseClusterCmd(*s.selectedCluster)
					case "grant":
						actionCmd = s.addIAMBindingCmd(*s.selectedCluster, s.pendingIAMRole, s.pendingIAMMember)
						s.viewState = ViewIAM
						s.pendingAction = ""
						return s, actionCmd
					case "kill-job":
						if s.selectedJob != nil {
							actionCmd = s.killJobCmd(*s.selectedJob)
						}
						s.viewState = ViewJobs
						s.pendingAction = ""
						return s, actionCmd
					}
				}
				s.viewState = s.actionSource
				return s, actionCmd
			case "n", "esc", "q":
				s.viewState = s.actionSource
				s.pendingAction = ""
				return s, nil
			}
		}
	}
	return s, nil
}

// -----------------------------------------------------------------------------
// Data & Helpers
// -----------------------------------------------------------------------------

// createClusterCmd fires the CreateCluster API call using the current form values.
func (s *Service) createClusterCmd() tea.Cmd {
	name := s.createForm.Value("Cluster Name")
	region := s.createForm.Value("Region")
	if region == "" {
		region = DefaultRegion
	}
	zone := s.createForm.Value("Zone")
	masterMachineType := s.createForm.Value("Master Machine Type")
	if masterMachineType == "" {
		masterMachineType = "n1-standard-2"
	}
	workerMachineType := s.createForm.Value("Worker Machine Type")
	if workerMachineType == "" {
		workerMachineType = "n1-standard-2"
	}
	numWorkers, err := strconv.Atoi(s.createForm.Value("Num Workers"))
	if err != nil || numWorkers <= 0 {
		numWorkers = 2
	}
	return func() tea.Msg {
		err := core.TrackJob(core.Job{
			ProjectID: s.projectID, Service: s.ShortName(), Resource: "cluster",
			Name: name, Action: "create",
		}, func() error {
			if s.client == nil {
				return fmt.Errorf("client not initialized")
			}
			return s.client.CreateCluster(s.projectID, region, name, zone, masterMachineType, workerMachineType, numWorkers)
		})
		if err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Cluster %s creating...", name)}
	}
}

// updateClusterCmd fires the UpdateClusterWorkerCount API call using the
// current update-form value.
func (s *Service) updateClusterCmd(cluster Cluster) tea.Cmd {
	numWorkers, err := strconv.Atoi(s.updateForm.Value("Num Workers"))
	if err != nil || numWorkers <= 0 {
		numWorkers = cluster.WorkerCount
	}
	return func() tea.Msg {
		err := core.TrackJob(core.Job{
			ProjectID: s.projectID, Service: s.ShortName(), Resource: "cluster",
			Name: cluster.Name, Action: "update",
		}, func() error {
			if s.client == nil {
				return fmt.Errorf("client not initialized")
			}
			return s.client.UpdateClusterWorkerCount(s.projectID, DefaultRegion, cluster.Name, numWorkers)
		})
		if err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Resizing cluster %s to %d workers...", cluster.Name, numWorkers)}
	}
}

// deleteClusterCmd triggers deletion of the given Dataproc cluster
func (s *Service) deleteClusterCmd(cluster Cluster) tea.Cmd {
	return func() tea.Msg {
		err := core.TrackJob(core.Job{
			ProjectID: s.projectID, Service: s.ShortName(), Resource: "cluster",
			Name: cluster.Name, Action: "delete",
		}, func() error {
			if s.client == nil {
				return fmt.Errorf("client not initialized")
			}
			return s.client.DeleteCluster(s.projectID, DefaultRegion, cluster.Name)
		})
		if err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Deleting cluster %s...", cluster.Name)}
	}
}

// startClusterCmd triggers starting the given (stopped) cluster
func (s *Service) startClusterCmd(cluster Cluster) tea.Cmd {
	return func() tea.Msg {
		err := core.TrackJob(core.Job{
			ProjectID: s.projectID, Service: s.ShortName(), Resource: "cluster",
			Name: cluster.Name, Action: "start",
		}, func() error {
			if s.client == nil {
				return fmt.Errorf("client not initialized")
			}
			return s.client.StartCluster(s.projectID, DefaultRegion, cluster.Name)
		})
		if err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Starting cluster %s...", cluster.Name)}
	}
}

// stopClusterCmd triggers stopping the given (running) cluster
func (s *Service) stopClusterCmd(cluster Cluster) tea.Cmd {
	return func() tea.Msg {
		err := core.TrackJob(core.Job{
			ProjectID: s.projectID, Service: s.ShortName(), Resource: "cluster",
			Name: cluster.Name, Action: "stop",
		}, func() error {
			if s.client == nil {
				return fmt.Errorf("client not initialized")
			}
			return s.client.StopCluster(s.projectID, DefaultRegion, cluster.Name)
		})
		if err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Stopping cluster %s...", cluster.Name)}
	}
}

// diagnoseClusterCmd kicks off diagnostic collection on cluster.
func (s *Service) diagnoseClusterCmd(cluster Cluster) tea.Cmd {
	return func() tea.Msg {
		err := core.TrackJob(core.Job{
			ProjectID: s.projectID, Service: s.ShortName(), Resource: "cluster",
			Name: cluster.Name, Action: "diagnose",
		}, func() error {
			if s.client == nil {
				return fmt.Errorf("client not initialized")
			}
			return s.client.DiagnoseCluster(s.projectID, DefaultRegion, cluster.Name)
		})
		if err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Diagnosing cluster %s...", cluster.Name)}
	}
}

// fetchIAMCmd fetches the current IAM policy for a cluster.
func (s *Service) fetchIAMCmd(cluster Cluster) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return iamPolicyMsg{err: fmt.Errorf("client not initialized")}
		}
		bindings, err := s.client.GetClusterIAMPolicy(s.projectID, DefaultRegion, cluster.Name)
		if err != nil {
			return iamPolicyMsg{err: err}
		}
		return iamPolicyMsg{bindings: bindings}
	}
}

// addIAMBindingCmd grants role to member on the given cluster.
func (s *Service) addIAMBindingCmd(cluster Cluster, role, member string) tea.Cmd {
	return func() tea.Msg {
		err := core.TrackJob(core.Job{
			ProjectID: s.projectID, Service: s.ShortName(), Resource: "cluster",
			Name: cluster.Name, Action: "grant",
		}, func() error {
			if s.client == nil {
				return fmt.Errorf("client not initialized")
			}
			return s.client.AddClusterIAMBinding(s.projectID, DefaultRegion, cluster.Name, role, member)
		})
		if err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Granted %s to %s on cluster %s", role, member, cluster.Name)}
	}
}

// fetchJobsCmd fetches the region's Dataproc jobs.
func (s *Service) fetchJobsCmd() tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return jobsMsg{err: fmt.Errorf("client not initialized")}
		}
		jobs, err := s.client.ListJobs(s.projectID, DefaultRegion)
		if err != nil {
			return jobsMsg{err: err}
		}
		return jobsMsg{jobs: jobs}
	}
}

// submitSparkJobCmd submits a minimal Spark job to cluster.
func (s *Service) submitSparkJobCmd(cluster Cluster, mainClass, jarURI string) tea.Cmd {
	return func() tea.Msg {
		err := core.TrackJob(core.Job{
			ProjectID: s.projectID, Service: s.ShortName(), Resource: "job",
			Name: cluster.Name, Action: "submit",
		}, func() error {
			if s.client == nil {
				return fmt.Errorf("client not initialized")
			}
			return s.client.SubmitSparkJob(s.projectID, DefaultRegion, cluster.Name, mainClass, jarURI)
		})
		if err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Submitted Spark job to %s", cluster.Name)}
	}
}

// killJobCmd cancels a running job.
func (s *Service) killJobCmd(job JobInfo) tea.Cmd {
	return func() tea.Msg {
		err := core.TrackJob(core.Job{
			ProjectID: s.projectID, Service: s.ShortName(), Resource: "job",
			Name: job.ID, Action: "kill",
		}, func() error {
			if s.client == nil {
				return fmt.Errorf("client not initialized")
			}
			return s.client.KillJob(s.projectID, DefaultRegion, job.ID)
		})
		if err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Killing job %s...", job.ID)}
	}
}

func (s *Service) updateJobTable(jobs []JobInfo) {
	rows := make([]table.Row, len(jobs))
	for i, j := range jobs {
		rows[i] = table.Row{j.ID, j.Type, j.State}
	}
	s.jobTable.SetRows(rows)
}

func (s *Service) fetchClustersCmd(force bool) tea.Cmd {
	return func() tea.Msg {
		key := fmt.Sprintf("dataproc:%s:%s", s.projectID, DefaultRegion)
		if !force && s.cache != nil {
			if val, found := s.cache.Get(key); found {
				if items, ok := val.([]Cluster); ok {
					return clustersMsg(items)
				}
			}
		}
		if s.client == nil {
			return errMsg(fmt.Errorf("client not init"))
		}
		items, err := s.client.ListClusters(s.projectID, DefaultRegion)
		if err != nil {
			return errMsg(err)
		}
		if s.cache != nil {
			s.cache.Set(key, items, CacheTTL)
		}
		return clustersMsg(items)
	}
}

func (s *Service) updateTable(items []Cluster) {
	rows := make([]table.Row, len(items))
	for i, item := range items {
		rows[i] = table.Row{
			item.Name,
			item.Status,
			fmt.Sprintf("%d", item.WorkerCount),
			item.Zone,
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
		return components.ContainsMatch(cluster.Name, cluster.Status, cluster.Zone)(q)
	})
}
