package gke

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
	"github.com/yogirk/tgcp/internal/styles"
	"github.com/yogirk/tgcp/internal/ui/components"
)

// renderConfirmation renders the delete confirmation dialog. Cluster delete
// requires a second confirmation ("delete-confirm2") because it is
// irreversible and destroys every node pool and workload in the cluster.
func (s *Service) renderConfirmation() string {
	if s.selectedCluster == nil {
		return "Error: No cluster selected"
	}
	if s.pendingAction == "delete-confirm2" {
		return components.RenderConfirmationWithMessage(
			"delete",
			s.selectedCluster.Name,
			"cluster",
			fmt.Sprintf("FINAL WARNING: this will permanently destroy cluster %s and every node pool/workload running on it.", s.selectedCluster.Name),
		)
	}
	if s.pendingAction == "master-upgrade" {
		return components.RenderConfirmationWithMessage(
			"master-upgrade",
			s.selectedCluster.Name,
			"cluster",
			fmt.Sprintf("Upgrade the control plane of cluster %s to %s?", s.selectedCluster.Name, s.pendingVersion),
		)
	}
	if s.pendingAction == "nodepool-upgrade" && len(s.selectedCluster.NodePools) > 0 {
		return components.RenderConfirmationWithMessage(
			"nodepool-upgrade",
			s.selectedCluster.NodePools[0].Name,
			"node pool",
			fmt.Sprintf("Upgrade node pool %s to %s?", s.selectedCluster.NodePools[0].Name, s.pendingVersion),
		)
	}
	return components.RenderConfirmation(s.pendingAction, s.selectedCluster.Name, "cluster")
}

func (s *Service) renderDetailView() string {
	if s.selectedCluster == nil {
		return "No cluster selected"
	}
	c := s.selectedCluster

	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Clusters",
		c.Name,
	)

	headerBox := components.DetailCard(components.DetailCardOpts{
		Title: "Cluster Details",
		Rows: []components.KeyValue{
			{Key: "Name", Value: c.Name},
			{Key: "Status", Value: c.Status},
			{Key: "Location", Value: c.Location},
			{Key: "Node Count", Value: fmt.Sprintf("%d", c.NodeCount)},
			{Key: "Master", Value: c.MasterVersion},
			{Key: "Endpoint", Value: c.Endpoint},
			{Key: "Mode", Value: c.Mode},
			{Key: "Network", Value: c.Network},
			{Key: "Subnetwork", Value: c.Subnetwork},
		},
	})

	// 2. Node Pools Box
	var poolLines []string
	for _, p := range c.NodePools {
		spotLabel := ""
		if p.IsSpot {
			spotLabel = styles.WarningStyle.Render(" SPOT")
		}

		poolParams := fmt.Sprintf(
			"  Type: %s | Disk: %dGB | Count: %d (Init: %d)",
			p.MachineType, p.DiskSizeGb, p.InitialNodeCount, p.InitialNodeCount,
		)

		autoScaling := ""
		if p.Autoscaling.Enabled {
			autoScaling = fmt.Sprintf("  Autoscaling: %d - %d nodes", p.Autoscaling.MinNodeCount, p.Autoscaling.MaxNodeCount)
		}

		line := fmt.Sprintf("%s %s%s\n%s", components.RenderStatus(p.Status), p.Name, spotLabel, poolParams)
		if autoScaling != "" {
			line += "\n" + autoScaling
		}
		poolLines = append(poolLines, line, "") // Empty string for spacing
	}

	poolsContent := lipgloss.JoinVertical(lipgloss.Left, poolLines...)
	poolsBox := components.DetailSection("Node Pools (Infrastructure Cost)", poolsContent, styles.ColorBorderSubtle)

	// 3. Command Hint
	cmdHint := styles.SubtextStyle.Render("Press 'K' to launch k9s for this cluster")

	return lipgloss.JoinVertical(lipgloss.Left,
		breadcrumb,
		"",
		headerBox,
		"",
		poolsBox,
		"",
		cmdHint,
	)
}
