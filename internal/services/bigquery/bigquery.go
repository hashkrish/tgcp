package bigquery

import (
	"context"
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/yogirk/tgcp/internal/core"
	"github.com/yogirk/tgcp/internal/ui/components"
)

const CacheTTL = 5 * time.Minute

// -----------------------------------------------------------------------------
// Models
// -----------------------------------------------------------------------------

type tickMsg time.Time

type ViewState int

const (
	ViewDatasets ViewState = iota
	ViewTables
	ViewSchema
	ViewCreate
	ViewUpdate
	ViewConfirmation
)

// newDatasetUpdateForm builds the FormModel for updating a dataset's
// description, seeded with its current value. Labels, access, and default
// table expiration are out of scope for this minimal Update flow.
func newDatasetUpdateForm(ds Dataset) components.FormModel {
	return components.NewForm("Update Dataset: "+ds.ID, []components.FormField{
		{Label: "Description", Default: ds.Description},
	})
}

type datasetsMsg []Dataset
type tablesMsg []Table
type schemaMsg []SchemaField
type errMsg error

// actionResultMsg carries the result of an async action (e.g. dataset creation)
type actionResultMsg struct {
	err error
	msg string
}

// -----------------------------------------------------------------------------
// Service Definition
// -----------------------------------------------------------------------------

type Service struct {
	client    *Client
	projectID string

	// Tables
	datasetTable *components.StandardTable
	tableTable   *components.StandardTable
	schemaTable  *components.StandardTable

	// State
	datasets []Dataset
	tables   []Table
	schema   []SchemaField
	spinner  components.SpinnerModel
	err      error

	viewState       ViewState
	selectedDataset *Dataset
	selectedTable   *Table

	createForm components.FormModel
	updateForm components.FormModel

	// Confirmation State
	pendingAction string    // "delete"
	actionSource  ViewState // Where to return after confirmation

	cache *core.Cache
}

func NewService(cache *core.Cache) *Service {
	// Dataset Table
	dsCols := []table.Column{
		{Title: "ID", Width: 30},
		{Title: "Location", Width: 15},
		{Title: "Labels", Width: 8},
		{Title: "Created", Width: 12},
		{Title: "Description", Width: 30},
	}
	dsTable := components.NewStandardTable(dsCols)

	// Table Table
	tCols := []table.Column{
		{Title: "ID", Width: 30},
		{Title: "Type", Width: 10},
		{Title: "Rows", Width: 10},
		{Title: "Size", Width: 10},
		{Title: "Partitioning", Width: 18},
		{Title: "Created", Width: 12},
	}
	tTable := components.NewStandardTable(tCols)

	// Schema Table
	sCols := []table.Column{
		{Title: "Field", Width: 20},
		{Title: "Type", Width: 15},
		{Title: "Mode", Width: 10},
		{Title: "Description", Width: 30},
	}
	sTable := components.NewStandardTable(sCols)

	return &Service{
		datasetTable: dsTable,
		tableTable:   tTable,
		schemaTable:  sTable,
		spinner:      components.NewSpinner(),
		viewState:    ViewDatasets,
		cache:        cache,
	}
}

func (s *Service) Name() string      { return "BigQuery" }
func (s *Service) ShortName() string { return "bq" }

func (s *Service) HelpText() string {
	if s.viewState == ViewDatasets {
		return "Ent:Select  r:Refresh  n:New Dataset  u:Update  d:Delete"
	}
	if s.viewState == ViewTables {
		return "Ent:Schema  Esc/q:Back"
	}
	if s.viewState == ViewSchema {
		return "Esc/q:Back"
	}
	if s.viewState == ViewConfirmation {
		return "y:Confirm  n:Cancel"
	}
	if s.viewState == ViewCreate || s.viewState == ViewUpdate {
		return "Tab/↑↓:Move  Enter/Ctrl+S:Submit  Esc:Cancel"
	}
	return ""
}

// -----------------------------------------------------------------------------
// Lifecycle
// -----------------------------------------------------------------------------

func (s *Service) InitService(ctx context.Context, projectID string) error {
	s.projectID = projectID
	client, err := NewClient(ctx, projectID)
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
	return tea.Batch(s.spinner.Start(""), s.fetchDatasetsCmd(), s.tick())
}

func (s *Service) tick() tea.Cmd {
	return tea.Tick(CacheTTL, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (s *Service) Refresh() tea.Cmd {
	var fetchCmd tea.Cmd
	if s.viewState == ViewDatasets {
		fetchCmd = s.fetchDatasetsCmd()
	} else if s.viewState == ViewTables && s.selectedDataset != nil {
		fetchCmd = s.fetchTablesCmd()
	}
	if fetchCmd == nil {
		return nil
	}
	return tea.Batch(
		s.spinner.Start(""),
		fetchCmd,
	)
}

func (s *Service) Reset() {
	s.viewState = ViewDatasets
	s.selectedDataset = nil
	s.selectedTable = nil
	s.err = nil
	s.datasetTable.SetCursor(0)
}

func (s *Service) IsRootView() bool {
	return s.viewState == ViewDatasets
}

func (s *Service) Focus() {
	s.datasetTable.Focus()
	s.tableTable.Focus()
	s.schemaTable.Focus()
}

func (s *Service) Blur() {
	s.datasetTable.Blur()
	s.tableTable.Blur()
	s.schemaTable.Blur()
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
		// Background refresh? Maybe only if at root.
		if s.viewState == ViewDatasets {
			return s, tea.Batch(s.fetchDatasetsCmd(), s.tick())
		}
		return s, s.tick()

	case datasetsMsg:
		s.spinner.Stop()
		s.datasets = msg
		s.updateDatasetTable()
		return s, func() tea.Msg { return core.LastUpdatedMsg(time.Now()) }

	case tablesMsg:
		s.spinner.Stop()
		s.tables = msg
		s.updateTableTable()
		return s, func() tea.Msg { return core.LastUpdatedMsg(time.Now()) }

	case schemaMsg:
		s.spinner.Stop()
		s.schema = msg
		s.updateSchemaTable()
		return s, func() tea.Msg { return core.LastUpdatedMsg(time.Now()) }

	case errMsg:
		s.spinner.Stop()
		s.err = msg
		return s, nil

	case actionResultMsg:
		if s.pendingAction == "delete" {
			s.pendingAction = ""
			if msg.err != nil {
				return s, func() tea.Msg {
					return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
				}
			}
			s.selectedDataset = nil
			s.viewState = ViewDatasets
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
		s.viewState = ViewDatasets
		return s, tea.Batch(
			func() tea.Msg {
				return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess}
			},
			s.Refresh(),
		)

	case tea.WindowSizeMsg:
		s.datasetTable.HandleWindowSizeDefault(msg)
		s.tableTable.HandleWindowSizeDefault(msg)
		s.schemaTable.HandleWindowSizeDefault(msg)

	case tea.MouseMsg:
		// Forward mouse events to active table for click selection
		switch s.viewState {
		case ViewDatasets:
			var updatedTable *components.StandardTable
			updatedTable, cmd = s.datasetTable.Update(msg)
			s.datasetTable = updatedTable
			return s, cmd
		case ViewTables:
			var updatedTable *components.StandardTable
			updatedTable, cmd = s.tableTable.Update(msg)
			s.tableTable = updatedTable
			return s, cmd
		}

	case tea.KeyMsg:
		if s.viewState == ViewCreate {
			result, formCmd := s.createForm.Update(msg)
			if result.Cancelled {
				s.viewState = ViewDatasets
				return s, nil
			}
			if result.Submitted {
				return s, s.createDatasetCmd()
			}
			return s, formCmd
		}

		if s.viewState == ViewUpdate {
			result, formCmd := s.updateForm.Update(msg)
			if result.Cancelled {
				s.viewState = ViewDatasets
				return s, nil
			}
			if result.Submitted && s.selectedDataset != nil {
				return s, s.updateDatasetCmd(*s.selectedDataset)
			}
			return s, formCmd
		}

		if s.viewState == ViewConfirmation {
			switch msg.String() {
			case "y", "enter":
				var actionCmd tea.Cmd
				if s.pendingAction == "delete" && s.selectedDataset != nil {
					actionCmd = s.deleteDatasetCmd(*s.selectedDataset)
				}
				s.viewState = s.actionSource
				return s, actionCmd
			case "n", "esc", "q":
				s.viewState = s.actionSource
				s.pendingAction = ""
				return s, nil
			}
			return s, nil
		}

		switch msg.String() {
		case "r":
			return s, s.Refresh()
		}

		if s.viewState == ViewDatasets {
			if msg.String() == "n" {
				s.createForm = components.NewForm("Create Dataset", []components.FormField{
					{Label: "Dataset ID", Placeholder: "my_dataset", Required: true},
					{Label: "Location", Placeholder: "US", Default: "US"},
				})
				s.viewState = ViewCreate
				return s, nil
			}
			if msg.String() == "enter" {
				if s.datasetTable.Cursor() >= 0 && s.datasetTable.Cursor() < len(s.datasets) {
					s.selectedDataset = &s.datasets[s.datasetTable.Cursor()]
					s.viewState = ViewTables
					return s, tea.Batch(s.fetchTablesCmd(), s.spinner.Start(""))
				}
			}
			if msg.String() == "u" {
				if s.datasetTable.Cursor() >= 0 && s.datasetTable.Cursor() < len(s.datasets) {
					ds := s.datasets[s.datasetTable.Cursor()]
					s.selectedDataset = &ds
					s.updateForm = newDatasetUpdateForm(ds)
					s.viewState = ViewUpdate
					return s, nil
				}
			}
			if msg.String() == "d" {
				if s.datasetTable.Cursor() >= 0 && s.datasetTable.Cursor() < len(s.datasets) {
					ds := s.datasets[s.datasetTable.Cursor()]
					s.selectedDataset = &ds
					s.pendingAction = "delete"
					s.actionSource = ViewDatasets
					s.viewState = ViewConfirmation
					return s, nil
				}
			}
			var updatedTable *components.StandardTable
			updatedTable, cmd = s.datasetTable.Update(msg)
			s.datasetTable = updatedTable
			return s, cmd
		}

		if s.viewState == ViewTables {
			if msg.String() == "esc" || msg.String() == "q" {
				s.viewState = ViewDatasets
				s.selectedDataset = nil
				return s, nil
			}
			if msg.String() == "enter" {
				if s.tableTable.Cursor() >= 0 && s.tableTable.Cursor() < len(s.tables) {
					s.selectedTable = &s.tables[s.tableTable.Cursor()]
					s.viewState = ViewSchema
					return s, tea.Batch(s.fetchSchemaCmd(), s.spinner.Start(""))
				}
			}
			var updatedTable *components.StandardTable
			updatedTable, cmd = s.tableTable.Update(msg)
			s.tableTable = updatedTable
			return s, cmd
		}

		if s.viewState == ViewSchema {
			if msg.String() == "esc" || msg.String() == "q" {
				s.viewState = ViewTables
				s.selectedTable = nil
				return s, nil
			}
			var updatedTable *components.StandardTable
			updatedTable, cmd = s.schemaTable.Update(msg)
			s.schemaTable = updatedTable
			return s, cmd
		}
	}
	return s, nil
}

// -----------------------------------------------------------------------------
// View
// -----------------------------------------------------------------------------

func (s *Service) View() string {
	if s.err != nil {
		return components.RenderError(s.err, s.Name(), "Datasets")
	}

	// Show spinner while loading
	if s.spinner.IsActive() {
		return s.spinner.View()
	}

	switch s.viewState {
	case ViewCreate:
		return s.createForm.View()
	case ViewUpdate:
		return s.updateForm.View()
	case ViewConfirmation:
		return s.renderConfirmation()
	case ViewDatasets:
		breadcrumb := components.Breadcrumb(
			fmt.Sprintf("Project %s", s.projectID),
			s.Name(),
			"Datasets",
		)
		return lipgloss.JoinVertical(lipgloss.Left, breadcrumb, s.datasetTable.View())
	case ViewTables:
		header := components.Breadcrumb(
			fmt.Sprintf("Project %s", s.projectID),
			s.Name(),
			"Datasets",
			s.selectedDataset.ID,
			"Tables",
		)
		return lipgloss.JoinVertical(lipgloss.Left, header, s.tableTable.View())
	case ViewSchema:
		header := components.Breadcrumb(
			fmt.Sprintf("Project %s", s.projectID),
			s.Name(),
			"Datasets",
			s.selectedDataset.ID,
			"Tables",
			s.selectedTable.ID,
			"Schema",
		)
		return lipgloss.JoinVertical(lipgloss.Left, header, s.schemaTable.View())
	}
	return ""
}

// renderConfirmation renders the dataset-delete confirmation dialog.
func (s *Service) renderConfirmation() string {
	if s.selectedDataset == nil {
		return "Error: No dataset selected"
	}
	return components.RenderConfirmationWithMessage(
		s.pendingAction,
		s.selectedDataset.ID,
		"dataset",
		fmt.Sprintf("Are you sure you want to DELETE dataset %s? Only empty datasets can be deleted — this app does not support deleting tables.", s.selectedDataset.ID),
	)
}

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

// deleteDatasetCmd triggers deletion of the given dataset. The BigQuery API
// itself refuses to delete a non-empty dataset (matching `bq rm -d` without
// -f/--recursive), which is the safety behavior wanted here.
func (s *Service) deleteDatasetCmd(ds Dataset) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.DeleteDataset(ds.ID); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Deleting dataset %s...", ds.ID)}
	}
}

// createDatasetCmd fires the CreateDataset API call using the current form values.
func (s *Service) createDatasetCmd() tea.Cmd {
	id := s.createForm.Value("Dataset ID")
	location := s.createForm.Value("Location")
	if location == "" {
		location = "US"
	}
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.CreateDataset(id, location); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Dataset %s created", id)}
	}
}

// updateDatasetCmd fires the UpdateDatasetDescription API call using the
// current update-form value.
func (s *Service) updateDatasetCmd(ds Dataset) tea.Cmd {
	description := s.updateForm.Value("Description")
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.UpdateDatasetDescription(ds.ID, description); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Updating dataset %s...", ds.ID)}
	}
}

func (s *Service) fetchDatasetsCmd() tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return errMsg(fmt.Errorf("client not init"))
		}
		ds, err := s.client.ListDatasets(s.projectID)
		if err != nil {
			return errMsg(err)
		}
		return datasetsMsg(ds)
	}
}

func (s *Service) fetchTablesCmd() tea.Cmd {
	return func() tea.Msg {
		if s.selectedDataset == nil {
			return errMsg(fmt.Errorf("no dataset"))
		}
		tables, err := s.client.ListTables(s.selectedDataset.ID)
		if err != nil {
			return errMsg(err)
		}
		return tablesMsg(tables)
	}
}

func (s *Service) fetchSchemaCmd() tea.Cmd {
	return func() tea.Msg {
		if s.selectedTable == nil {
			return errMsg(fmt.Errorf("no table"))
		}
		fields, err := s.client.GetTableSchema(s.selectedDataset.ID, s.selectedTable.ID)
		if err != nil {
			return errMsg(err)
		}
		return schemaMsg(fields)
	}
}

func (s *Service) updateDatasetTable() {
	rows := make([]table.Row, len(s.datasets))
	for i, d := range s.datasets {
		created := ""
		if !d.CreationTime.IsZero() {
			created = d.CreationTime.Format("2006-01-02")
		}
		rows[i] = table.Row{d.ID, d.Location, fmt.Sprintf("%d", len(d.Labels)), created, d.Description}
	}
	s.datasetTable.SetRows(rows)
}

func (s *Service) updateTableTable() {
	rows := make([]table.Row, len(s.tables))
	for i, t := range s.tables {
		size := fmt.Sprintf("%d B", t.TotalBytes)
		if t.TotalBytes > 1024*1024*1024 {
			size = fmt.Sprintf("%.1f GB", float64(t.TotalBytes)/1024/1024/1024)
		} else if t.TotalBytes > 1024*1024 {
			size = fmt.Sprintf("%.1f MB", float64(t.TotalBytes)/1024/1024)
		}

		created := ""
		if !t.CreationTime.IsZero() {
			created = t.CreationTime.Format("2006-01-02")
		}

		rows[i] = table.Row{t.ID, t.Type, fmt.Sprintf("%d", t.NumRows), size, t.Partitioning, created}
	}
	s.tableTable.SetRows(rows)
}

func (s *Service) updateSchemaTable() {
	rows := make([]table.Row, len(s.schema))
	for i, f := range s.schema {
		rows[i] = table.Row{f.Name, f.Type, f.Mode, f.Description}
	}
	s.schemaTable.SetRows(rows)
}
